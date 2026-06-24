# 本地 Skill 系统

正常启动只确保二进制同目录 `skills/` 存在，不下载、不生成、不安装外部 skill。目录格式：

```text
skills/<name>/
  SKILL.md
  references/   # optional
  scripts/      # optional
  assets/       # optional
```

`SKILL.md` 必须有 `name` 与 `description` frontmatter，且 name 与目录名一致。启动只构建轻量索引；`skill_view` 选择后才读取正文或允许类型的相对资源。

扫描会拒绝目录穿越、软链接、缺失/损坏 frontmatter、重复名称、超大文件、索引超限和越界资源。错误 skill 被隔离，不阻塞其他 skill 或程序启动。

配置项：`ELIZA_SKILLS_ENABLED`、`ELIZA_SKILLS_DISABLED`、`ELIZA_SKILL_MAX_FILE_BYTES`、`ELIZA_SKILL_MAX_INDEX_BYTES`。

TUI 支持 `/skills`、`/skills reload`、`/skills enable <name>`、`/skills disable <name>`。启停仅改变当前加载策略，不修改用户文件。skill 内容带 `UNTRUSTED SKILL` 边界，不能提升工具、memory 或 workspace 权限。
