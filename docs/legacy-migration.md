# 旧站数据迁移

把旧版单点登录（`accounts_mcii_cc`）和订阅共享（`proxy_mcii_cc`）两个数据库一次性导入当前站点。**正式导入会清空当前站点除运行设置（`np_settings`）以外的全部数据**，只保留旧站内容。

## 准备

1. 停止旧站写入（或切到只读），导出最新数据并导入到可连接的 MySQL／MariaDB（可以和当前站点同一实例，库名任意）。
2. 停止当前站点服务，备份目标库。
3. 准备环境变量：
   - `MYSQL_HOST` / `MYSQL_PORT` / `MYSQL_USER` / `MYSQL_PASSWORD` / `MYSQL_DATABASE`：当前站点的目标库，与服务使用的相同。
   - `NODEPOOL_DATA_KEY`：与服务使用的相同，导入的来源地址、内容和登录密钥会用它重新加密。
   - `LEGACY_SUB_SECRET`：旧订阅共享站的 `SUB_SECRET`，用于解密来源订阅地址。不要写进仓库或命令历史。
   - `ADMIN_PASSWORD`：迁移后管理员首次登录用的密码（服务启动时写入 id=1 账号）。

## 执行

`nodepool` 是后端程序，需要先编译（或使用 Docker 镜像里的 `/app/nodepool`）。Windows 本地编译：

```powershell
cd backend
go build -o ..\nodepool.exe ./cmd/nodepool
```

DSN 格式为 `用户:密码@tcp(主机:端口)/库名`，例如 `root:pass@tcp(127.0.0.1:3306)/accounts_mcii_cc`。

先试运行，只读取旧库并输出计划，不写入任何数据（试运行可以不设置 `MYSQL_*`）：

```bash
nodepool migrate-legacy \
  -accounts-dsn 'user:pass@tcp(host:3306)/accounts_mcii_cc' \
  -proxy-dsn 'user:pass@tcp(host:3306)/proxy_mcii_cc' \
  -admin-email 2242299685@qq.com \
  -dry-run -report dry-run.json
```

核对报告后正式导入（加 `-avatars` 会并发下载 GitHub／LINUX DO 头像，失败的留空）：

```bash
nodepool migrate-legacy -accounts-dsn '…' -proxy-dsn '…' -admin-email 2242299685@qq.com -yes -report import.json
```

Docker 部署可以用同一镜像运行：`docker compose run --rm -e LEGACY_SUB_SECRET nodepool /app/nodepool migrate-legacy …`（`-e LEGACY_SUB_SECRET` 从当前 shell 传入密钥，容器需要能连到旧库）。导入完成后再启动服务。

## 对应关系

| 旧数据 | 迁移后 |
| --- | --- |
| `-admin-email` 对应的账号 | id=1，管理员；密码由 `ADMIN_PASSWORD` 设置 |
| 其他 `is_admin=1` 账号 | 用户管理员（`manager`），保留原 ID |
| 普通账号 | 普通用户，保留原 ID；没有用户名的用 `user<ID>` |
| 密码（argon2id／bcrypt） | **不迁移**。用户用旧密码登录时会提示：有邮箱的用邮箱验证码设置新密码并自动登录；有第三方绑定的用 GitHub／LINUX DO 登录后强制设置密码 |
| 邮箱 | 标记为已验证 |
| `disabled_at`、`profiles.banned` | 账号停用 |
| 经验、积分、签到日期与连签 | 原样保留；流水全部导入（`checkin` 签到，`legacy_linuxdo`、`legacy_provide`、`legacy_debit` 为旧站类型） |
| `oauth_bindings` | 第三方绑定；同一账号同平台有多个绑定时保留最近一次 |
| 未删除的来源 | 保留原 ID 和直链令牌；`live` 启用，`draft`／`down` 停用；人数上限 0 或 999999999 视为不限；订阅地址解密后重新加密 |
| 没有订阅地址的来源 | 以旧快照转为本地文件来源 |
| 已删除的来源 | 不迁移 |
| 来源快照 `body_yaml` | 直接写入节点，下次拉取时间按来源分散在刷新周期（12 小时）内排队；没有快照的来源在 1 小时内陆续首次拉取 |
| 有效席位 | 一条“跟随整个来源”的组合订阅，**沿用旧订阅令牌**（旧链接继续可用），租期从导入时起算；旧站关闭分流包装的用户导出纯节点，其余使用官方默认规则 |
| 领取／切换／取消／超时记录 | 席位记录（切换拆成旧来源释放＋新来源领取）；后台封禁记录不迁移 |
| 关注 | 关注提供方 |
| 登录设置 | 邮箱后缀、SMTP、GitHub／LINUX DO OAuth；密钥加密保存，关闭“强制绑定邮箱” |
| 登录状态、会话、两步验证、支付密码 | 不迁移 |

旧站来源直链 `/l/{token}`、`/give/{token}` 与当前的 `/link/{token}` 等价。

## 迁移后

- 第三方登录回调地址改为当前站点：GitHub 和 LINUX DO 应用里分别填 `https://<站点>/api/v1/auth/oauth/github/callback`、`…/linuxdo/callback`。
- 试运行报告的 `users_without_login` 列出既没有邮箱也没有第三方绑定的账号，需要管理员在用户管理里为其设置密码或邮箱。
- 旧站没有成就，导入时为来源提供方预先记一条零经验的成就流水，避免首次刷新来源时补发经验。
