# Mihomo NodePool

基于 Mihomo 的节点管理网页，使用 Vue 3、TypeScript、Go 和现有 MySQL，采用 Docker 部署。

**当前为准备骨架，不是完成的节点管理器。** 已提供真实系统状态页面、MySQL 连接配置、健康检查、前端嵌入构建和容器/发布流程。节点导入、去重、6 小时错峰检测、Mihomo 接入和网页更新器尚待实现。

完整目标见 [已确认需求](docs/requirements.md)。程序回退按要求只切换程序文件，不处理数据库。

## 本地开发

要求 Go 1.26+、Node.js 24 和 npm。

```bash
npm ci --prefix frontend
cd backend
go mod download
go run ./cmd/nodepool
```

另开终端：

```bash
npm run dev --prefix frontend
```

打开 `http://127.0.0.1:5173`。开发服务器将 API 转发到 `127.0.0.1:8080`。没有配置 MySQL 时可以启动，页面明确显示“未配置”。本地 Go 程序不自动读取 `.env`；需要连接 MySQL 时，在启动它的终端设置对应环境变量。

构建包含前端的单文件程序：

```bash
node scripts/build.mjs
```

产物为 `build/nodepool`（Windows 为 `build/nodepool.exe`）。运行产物后打开 `http://127.0.0.1:8080`。

## Docker

将 `.env.example` 复制为 `.env`，填写现有 MySQL 的地址、用户、密码及数据库名。

```bash
docker compose up -d --build
```

这是从源码构建的准备版本。首次正式发布后，可改用 GHCR 镜像：

```bash
docker compose pull
docker compose up -d --no-build
```

应用端口由 `HTTP_PORT` 设置，默认 `8080`。数据目录挂载在 `/app/data`，MySQL 由外部提供。容器启动脚本准备目录后降权到 UID 1000 运行应用，`/app` 保持可写，供后续在线更新器替换程序使用。

私有 GHCR 镜像需要在部署机器登录有读取该包权限的账号。当前尚未创建版本标签或发布镜像，`latest` 暂不可拉取。

## 配置

| 变量 | 默认值 | 作用 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | 应用监听地址 |
| `DATA_DIR` | `./data` | 本地数据目录，容器内为 `/app/data` |
| `MYSQL_HOST` | 空 | 现有数据库地址 |
| `MYSQL_PORT` | `3306` | 数据库端口 |
| `MYSQL_USER` | 空 | 数据库用户 |
| `MYSQL_PASSWORD` | 空 | 数据库密码，保留原始空格 |
| `MYSQL_DATABASE` | 空 | 数据库名称 |
| `MYSQL_TLS` | `false` | 设置 `true` 使用验证证书的 TLS 连接 |
| `UPDATE_GITHUB_TOKEN` | 空 | 预留给后续私有 Release 下载，当前未使用 |

一旦配置 MySQL，`MYSQL_HOST`、`MYSQL_USER`、`MYSQL_DATABASE` 必须完整填写。骨架只检查连接，不创建或修改数据库表，也不运行迁移。页面与 API 不返回连接凭据。

## 检查

```bash
npm run typecheck --prefix frontend
node scripts/build.mjs
cd backend
go test ./...
go vet ./...
```

- `GET /healthz`：应用存活检查，Docker 使用此接口。
- `GET /readyz`：MySQL 连接检查；未配置或不可达时返回 503。当前不代表节点业务或内核就绪。
- `GET /api/v1/system`：真实版本、数据库连接状态与内核接入状态。

## 发布

普通提交触发 CI，检查前端、Go 和容器启动。推送稳定版本标签 `vX.Y.Z` 才触发 Release 工作流：

1. 构建并检查应用。
2. 构建 Linux AMD64、ARM64 程序包和 `checksums.txt`。
3. 发布 GHCR 多架构镜像，标签包括固定版本、`latest` 和提交号。
4. 创建 GitHub Release 并上传发行附件。

两条工作流使用固定提交版本的官方 Actions。当前仅准备工作流；完整发布链路要在首次发版时验证。网页更新/回退计划沿用参考项目的“下载并替换二进制 + Docker 重启”方式，尚未接入。容器重建将使用镜像中的程序版本。

## 目录

```text
backend/cmd/nodepool       程序入口与健康检查命令
backend/internal/config   环境配置
backend/internal/database MySQL 连接
backend/internal/server   HTTP API
backend/internal/web      嵌入前端资源
backend/internal/core     待接入的内核边界
frontend                  Vue 网页
scripts                   本地组合构建
deploy                    容器启动脚本
.github/workflows         检查与版本发布
docs                      已确认需求
```
