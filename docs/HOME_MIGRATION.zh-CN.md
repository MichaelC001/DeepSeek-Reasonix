# macOS 用户目录迁移说明

这份文档说明如何把 Reasonix 在 macOS 上的用户数据，从历史目录完整迁移到当前文档推荐目录。

English: [macOS home directory migration](./HOME_MIGRATION.md)

## 一句话版本

先关闭桌面端，然后在终端执行：

```sh
reasonix migrate-home
```

确认预览结果没问题后，再执行：

```sh
reasonix migrate-home --apply
```

最后重新打开桌面端。

## 这个迁移解决什么问题

Reasonix 早期 macOS 版本使用系统原生配置目录：

```text
~/Library/Application Support/reasonix
```

现在新安装默认使用文档里更容易理解、也更方便用户查看的目录：

```text
~/.config/reasonix
```

为了兼容老用户，Reasonix 会继续读取旧目录，所以升级后历史会话不会突然消失。`reasonix migrate-home` 的作用是把旧目录里的全部 Reasonix 用户数据完整搬到新目录，并在成功后让 CLI 和桌面端都改用新目录。

## 谁需要执行

如果你满足下面条件，建议执行迁移：

- 你在 macOS 上用过旧版本 Reasonix。
- 你的机器上存在 `~/Library/Application Support/reasonix`。
- 你希望以后配置、会话、桌面状态都统一放在 `~/.config/reasonix`。

如果你是新用户，或者一直通过 `REASONIX_HOME` 指定了自定义目录，通常不需要执行这个迁移。

## 迁移前需要知道

这次迁移是保守的：

- 旧目录不会被删除。
- 默认命令只预览，不写文件。
- 只有加上 `--apply` 才真正迁移。
- 迁移全部成功后才会写入完成标记。
- 如果迁移中途失败，不会切换到新目录；修复问题后可以重新运行。

建议先关闭 Reasonix 桌面端。这样可以避免正在运行的 tab 继续持有旧会话文件路径。

## 推荐操作步骤

1. 关闭 Reasonix 桌面端。

2. 先预览迁移计划：

   ```sh
   reasonix migrate-home
   ```

   这个命令会显示源目录、目标目录、预计复制的文件数量、会合并的配置、会改名保留的会话冲突，以及会归档的未知冲突。

3. 确认无误后执行迁移：

   ```sh
   reasonix migrate-home --apply
   ```

4. 重新打开 Reasonix 桌面端。

5. 可选：检查诊断信息。

   ```sh
   reasonix doctor
   ```

## 会迁移哪些数据

迁移工具会处理整个 Reasonix 用户目录，包括但不限于：

- `config.toml`：用户配置。
- `credentials`：Reasonix 管理的密钥环境变量文件。
- `sessions/`：CLI 全局历史会话。
- `projects/*/sessions/`：桌面端项目会话。
- 桌面端状态：打开的 tab、最近 workspace、workspace 列表、窗口大小和位置。
- 记忆、缓存、metrics、install id、插件相关状态等其他 Reasonix 用户数据。

未知文件也会被处理。目标路径没有同名文件时直接复制；目标路径已有不同内容时，不会覆盖，而是把源文件归档到冲突目录。

## 同名配置怎么处理

如果目标目录已经有 `config.toml`，不会简单覆盖。

处理规则是：

- 只有源配置存在：复制到目标目录。
- 只有目标配置存在：保留目标配置。
- 两边内容完全一样：跳过。
- 两边都有且内容不同：合并，目标目录里的配置优先。

插件配置 `[[plugins]]` 会按 `name` 合并：同名插件以目标目录为准，源目录里独有的插件会保留。

合并前，两份原始配置都会备份到：

```text
~/.config/reasonix/.reasonix-home-migration-backups/<run-id>/
```

## 密钥文件怎么处理

`credentials` 也不会简单覆盖。

处理规则是：

- 目标目录已有的 key 优先。
- 源目录里目标没有的 key 会追加过去。
- 同名 key 不会用源目录覆盖目标目录。
- 合并前会备份两份原始文件。

## 会话冲突怎么处理

如果同名 `.jsonl` 会话在源目录和目标目录里内容不同，迁移工具不会覆盖目标会话。

源目录里的会话会用新文件名保存，例如：

```text
chat.migrated-20260612-120000-ab12cd34.jsonl
```

对应的桌面端标题、显示文本、meta、checkpoint 等 sidecar 文件也会尽量跟着改名迁移。这样历史会话不会因为同名而丢失。

## 其他同名文件怎么处理

如果是不认识的同名文件，且两边内容不同，迁移工具不会猜测如何合并，也不会覆盖目标文件。

源文件会被保存到：

```text
~/.config/reasonix/.reasonix-home-migration-conflicts/<run-id>/
```

迁移完成后，你可以手动查看这些文件。

## 桌面端如何使用迁移结果

桌面端不需要单独执行一个迁移工具。

`reasonix migrate-home --apply` 成功后，会在目标目录写入：

```text
~/.config/reasonix/.reasonix-home-migration.json
```

这个文件表示迁移已经完整成功。之后 CLI 和桌面端都会通过同一套路径规则使用新目录。

所以桌面端的正确操作是：

1. 迁移前关闭桌面端。
2. 用 CLI 执行预览和迁移。
3. 迁移完成后重新打开桌面端。

重新打开后，历史会话、项目列表、tab 状态和窗口状态应该继续可用，只是存储位置已经切到新目录。

## 如何确认迁移成功

可以检查完成标记：

```sh
ls "$HOME/.config/reasonix/.reasonix-home-migration.json"
```

也可以查看诊断：

```sh
reasonix doctor
```

然后打开桌面端，确认历史会话和项目会话仍然能看到。

## 如果迁移失败怎么办

如果迁移失败，Reasonix 不会写完成标记，也不会切换到新目录。

你可以：

1. 阅读命令输出里的错误。
2. 处理权限、磁盘空间或文件占用问题。
3. 再次执行：

   ```sh
   reasonix migrate-home --apply
   ```

旧目录会保留，所以失败后不会因为半次迁移而丢失原始数据。

## 如果临时想继续用旧目录

不建议通过删除新目录来回退。旧目录还在，如果只是临时想强制使用旧目录，可以设置 `REASONIX_HOME`：

```sh
export REASONIX_HOME="$HOME/Library/Application Support/reasonix"
reasonix chat
```

桌面端如果需要强制使用旧目录，也应该通过启动环境设置 `REASONIX_HOME`，而不是手工删除迁移后的文件。

## 常见问题

### 迁移后历史会话会消失吗

不会。迁移工具会复制全局会话、项目会话以及桌面端 sidecar 文件。同名冲突时会改名保留源会话，而不是覆盖。

### 旧目录会被删除吗

不会。旧目录会保留，方便你确认迁移结果或手动备份。

### 可以重复执行吗

可以。迁移成功后再次执行会检测到完成标记，并报告已经迁移。

### 目标目录已经有配置，会不会被覆盖

不会直接覆盖。`config.toml` 会合并，目标目录里的值优先；`credentials` 也会合并，目标目录里的 key 优先。

### 桌面端有没有按钮

目前没有。请先用 CLI 执行迁移。后续如果增加桌面端按钮，也应该调用同一个迁移逻辑。
