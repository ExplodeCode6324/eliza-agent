package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type InstanceLock struct {
	path string
	pid  int
}

type instanceLockMetadata struct {
	PID        int
	Executable string
	StartedAt  time.Time
}

func AcquireInstanceLock() (*InstanceLock, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("获取二进制路径: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("解析二进制路径: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}

	lockRoot := filepath.Join(os.TempDir(), "eliza-instance-locks")
	if err := os.MkdirAll(lockRoot, 0777); err != nil {
		return nil, fmt.Errorf("创建实例锁目录: %w", err)
	}
	_ = os.Chmod(lockRoot, 0777)

	pathHash := sha256.Sum256([]byte(executable))
	lockPath := filepath.Join(lockRoot, fmt.Sprintf("%x.lock", pathHash[:8]))
	lock := &InstanceLock{path: lockPath, pid: os.Getpid()}

	for attempt := 0; attempt < 3; attempt++ {
		if err := os.Mkdir(lockPath, 0777); err == nil {
			_ = os.Chmod(lockPath, 0777)
			metadata := instanceLockMetadata{
				PID:        lock.pid,
				Executable: executable,
				StartedAt:  time.Now(),
			}
			if err := writeInstanceMetadata(lockPath, metadata); err != nil {
				_ = os.RemoveAll(lockPath)
				return nil, err
			}
			return lock, nil
		} else if !os.IsExist(err) {
			return nil, fmt.Errorf("创建实例锁 %s: %w", lockPath, err)
		}

		metadata, readErr := readInstanceMetadata(lockPath)
		if readErr != nil {
			// 另一个进程可能刚创建目录、尚未写完 metadata。
			time.Sleep(100 * time.Millisecond)
			metadata, readErr = readInstanceMetadata(lockPath)
		}
		if readErr != nil {
			return nil, fmt.Errorf("实例锁存在但元数据不可读，拒绝抢占 %s: %w", lockPath, readErr)
		}
		if processAlive(metadata.PID) {
			return nil, fmt.Errorf(
				"当前 eliza 二进制已被 PID %d 占用；同一二进制不允许多用户同时运行。需要并行使用时，请复制另一份 eliza 二进制",
				metadata.PID,
			)
		}

		if err := os.RemoveAll(lockPath); err != nil {
			return nil, fmt.Errorf("清理失效实例锁 %s: %w", lockPath, err)
		}
	}
	return nil, fmt.Errorf("无法获取实例锁 %s", lockPath)
}

func writeInstanceMetadata(lockPath string, metadata instanceLockMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("编码实例锁: %w", err)
	}
	metadataPath := filepath.Join(lockPath, "owner.json")
	if err := os.WriteFile(metadataPath, data, 0666); err != nil {
		return fmt.Errorf("写入实例锁: %w", err)
	}
	_ = os.Chmod(metadataPath, 0666)
	return nil
}

func readInstanceMetadata(lockPath string) (instanceLockMetadata, error) {
	var metadata instanceLockMetadata
	data, err := os.ReadFile(filepath.Join(lockPath, "owner.json"))
	if err != nil {
		return metadata, err
	}
	err = json.Unmarshal(data, &metadata)
	return metadata, err
}

func (l *InstanceLock) Release() {
	if l == nil {
		return
	}
	metadata, err := readInstanceMetadata(l.path)
	if err == nil && metadata.PID != l.pid {
		return
	}
	_ = os.RemoveAll(l.path)
}
