# Mihomo NodePool

中文节点管理网页，使用 Vue 3、TypeScript、Go、内嵌 Mihomo 和外部 MySQL。通过 Docker 单容器部署。

## 功能

- 导入订阅 URL、本地 Clash YAML/JSON 文件、Base64 订阅和 Mihomo 支持的节点分享链接；支持批量来源。
- 按完整连接配置生成指纹，跨来源去重。节点名称不参与指纹，密码、UUID 和传输参数参与；来源与原始名称保留。
- 节点可切换列表与卡片，支持分页、名称、地址、协议、国家、来源、健康状态和最大延迟筛选。国旗 SVG 随应用打包，出口显示 IP 及“国家 · 省／州 · 城市”。
- 全局与单来源名称排除正则；默认排除剩余流量、到期说明等信息节点。被排除节点不进入有效列表。
- 订阅刷新和唯一节点检测分别按各自加入时间每 6 小时执行，稳定错峰、限并发、持久化队列与任务租约，重启后恢复。没有“刷新全部订阅”操作。
- 使用内嵌 Mihomo URLTest、统一延迟、失败重试、连续失败/恢复阈值和大面积失败保护。通过对应节点查询出口 IP 与国家，不包含 AI 平台解锁检测。
- 单管理员密码登录。后续再开放多用户、注册，以及选择节点生成 `/sub/…` 合并订阅。
- 版本菜单读取 GitHub Release，支持下载校验后替换程序、回退和立即重启；**回退只切换程序，不处理数据库**。
- 下拉选择使用自绘组件，支持键盘和触屏；浅色／深色首次跟随系统，手动选择后保存在浏览器中。
- 节点、来源和设置使用独立路由；分页、筛选及视图保存在 URL，支持直接访问、刷新和浏览器前进后退。

节点请求、国家查询的结果受部署机器的网络、节点可达性及第三方查询服务影响。列表中的延迟是代理 HTTP 测试延迟，不是 ICMP ping。

完整需求和接口见 [requirements](docs/requirements.md) 与 [API](docs/api.md)。

## Windows 本地运行

要求 Go 1.26+、Node.js 24 和 npm；数据库使用现有 MySQL 8.0+（CI 使用 MySQL 8.4）。

先准备配置：

```powershell
Copy-Item .env.example .env
```

编辑 `.env`，填写 `MYSQL_HOST`、`MYSQL_PORT`、`MYSQL_USER`、`MYSQL_PASSWORD`、`MYSQL_DATABASE`。建议设置至少 8 字节的 `ADMIN_PASSWORD`。然后执行：

```powershell
.\scripts\start.ps1 -Build
```

打开 `http://127.0.0.1:8080`。按 Ctrl+C 停止。后续不修改代码时可运行 `.\scripts\start.ps1`（去掉 `-Build`）。

未设置管理员密码时，首次启动会生成 `data/admin-password`，日志只显示路径；可在本机读取该文件登录。没有 MySQL 时可查看登录和系统连接提示，不能导入或保存节点。不要把临时演示数据当成已连接数据库。

程序读取当前工作目录 `.env`，已有进程环境变量优先；可用 `NODEPOOL_ENV_FILE` 指定文件。Windows 启动脚本自动使用项目根目录配置。

## 开发与构建

```bash
npm ci --prefix frontend
node scripts/build.mjs
```

产物为 `build/nodepool`，Windows 为 `build/nodepool.exe`，已包含前端和 Mihomo。运行产物即可访问网页。

开发时另开终端运行：

```bash
npm run dev --prefix frontend
```

前端地址 `http://127.0.0.1:5173`，代理 API 到 `127.0.0.1:8080`。后端仍运行上述构建产物；修改 Go 代码后重新构建并启动。

## Docker

`.env` 连接现有 MySQL；应用会在指定库中创建 `np_` 前缀表，需要建表和读写权限。从旧版本升级时会补充节点的 `region`、`city` 两列，需要 `ALTER` 权限；现有节点和历史继续保留。不会修改其他表。请使用独立数据库，并确保容器网络能访问该实例。

从源码构建：

```bash
docker compose up -d --build
```

有正式版本后，使用 GHCR 镜像：

```bash
docker compose pull
docker compose up -d --no-build
```

默认网页端口 8080，可用 `HTTP_PORT` 更改。持久化目录 `/app/data` 保存自动生成的管理员密码等运行文件；业务数据在 MySQL 中。容器以 UID 1000 运行应用，启动脚本只在准备目录时使用 root。

当前按单应用实例部署。不要同时启动多个实例来扩容；全局并发上限及更新操作按单实例管理。

## 设置与数据行为

- 全局排除正则在运行设置中编辑，来源可单独追加规则。正则变化使用已保存内容重算，不刷新全部订阅链接。
- 相同配置在不同来源中只显示一行，展开节点详情可以查看来源和原始名称。同地址不同凭据不会合并。
- 删除来源只移除该来源关系；其他来源引用的节点继续保留。停用来源后它独有的节点不进入有效列表和检测。
- 订阅抓取或解析失败保留上次节点，来源卡片显示错误；自动刷新失败 15 分钟后重试。
- 批量新节点首次检测在 30 秒窗口内错峰排队；后续固定 6 小时周期按记录创建时间及稳定偏移分布，错过周期只补一次。
- 国家、省／州和城市通过对应节点获取，正常缓存 24 小时；旧记录缺少地区时会在下一次检测补查。第三方查询服务失效不会将可用节点判为不可用。
- 健康状态默认连续 3 次失败标为不可用、连续 2 次成功恢复；首次成功从未知转为可用。至少 5 个节点的一批测试有 80% 失败时，记录错误但保留健康状态，避免本机故障造成整批误判。

## 配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | 应用监听地址 |
| `DATA_DIR` | `./data` | 本地持久化目录，容器内为 `/app/data` |
| `ADMIN_PASSWORD` | 自动生成 | 管理员密码，至少 8 字节 |
| `MYSQL_HOST` | 空 | 现有数据库地址 |
| `MYSQL_PORT` | `3306` | 数据库端口 |
| `MYSQL_USER` | 空 | 数据库用户 |
| `MYSQL_PASSWORD` | 空 | 数据库密码 |
| `MYSQL_DATABASE` | 空 | 数据库名 |
| `MYSQL_TLS` | `false` | `true` 启用证书验证的 TLS |
| `NODEPOOL_ENV_FILE` | `.env` | 本地配置文件路径 |
| `UPDATE_GITHUB_TOKEN` | 空 | 读取私有仓库 Release 的令牌，需有目标仓库只读权限 |

凭据不返回前端；来源 URL 与节点原配置不会由列表接口回显。来源内容和凭据存储在你配置的 MySQL 中，数据库备份与访问权限由部署者管理。

## 更新、回退与发布

左上角版本按钮可查看发布说明和历史版本。Linux 正式版本可下载对应架构发行包，验证 `checksums.txt`，保留本地 `.backup` 后原子替换运行程序。点击“立即重启”后进程退出，Docker 的 `restart: unless-stopped` 负责拉起。

Windows 本地版本可以查看发布信息，但不执行在线替换或重启。`dev` 构建不允许在线安装；使用正式 `vX.Y.Z` 构建才开放。

回退可以恢复本地上一次程序备份，或下载安装可用的历史版本。回退不回滚 MySQL。容器内替换的程序不会改变镜像：重新创建容器时会回到所选镜像版本，因此重建时请选择对应版本的镜像。

推送稳定标签 `vX.Y.Z` 触发工作流，生成 Linux AMD64/ARM64 发行包、校验文件，以及 GHCR 多架构镜像和 GitHub Release。普通提交和 PR 只运行 CI，不发布版本。首次真实发布与真实私有资产下载仍需发版后验证。

## 验证

```bash
npm run typecheck --prefix frontend
node scripts/build.mjs
cd backend
go test ./...
go vet ./...
```

真实数据库测试仅允许使用名称以 `nodepool_test` 开头的隔离测试库。设置 `MYSQL_TEST_DSN` 后运行；容量用例额外设置 `NODEPOOL_CAPACITY_TEST=1`。这些用例会清空测试库中的 `np_` 数据，禁止指向业务库。

浏览器测试位于 `e2e/`，运行前构建应用、安装测试依赖及 Chromium，并设置连接隔离库 `nodepool_browser_test` 的 MySQL 环境变量。使用 `node e2e/smoke.mjs`，测试覆盖登录、设置、三种来源导入、去重、排除、搜索、真实代理延迟、来源删除、移动布局和退出。

- `/healthz`：无需登录的存活检查。
- `/readyz`：数据库和表结构就绪检查。
- `/api/v1/system`：登录后获取版本、内核、数据库和任务状态。

## 许可

本项目内嵌 Mihomo，按 GNU GPLv3 提供。见 [LICENSE](LICENSE) 和 [NOTICE](NOTICE)。
