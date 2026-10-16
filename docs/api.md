# API 契约

业务 API 使用 `/api/v1` 前缀并返回 JSON，错误为 `{ "error": "中文说明" }`。除认证状态、登录及健康检查外，业务 API 需要登录 Cookie；客户端订阅使用独立 `/sub/{token}` 入口，无需 Cookie。暂不开放自助注册。

常用错误：400 输入无效，401 未登录或会话失效，403 越权，404 对象或令牌不存在，409 额度／领取名额冲突，422 无可导出节点，503 数据库不可用。

## 账号与认证

`User = {id,username,display_name,role:"admin"|"user",enabled,node_limit,source_limit,subscription_limit,created_at}`。

- GET `/auth` → `{authenticated,username,user:User|null}`。
- POST `/auth/login` `{username?,password}` → 同上，并设置 HttpOnly、SameSite Cookie；省略用户名兼容 `admin`。
- POST `/auth/logout` → `{authenticated:false}`。
- GET `/me` → `{user:User,usage:{nodes,sources,subscriptions},provider_path}`。
- PATCH `/me` `{display_name?,password?}` → User；修改密码撤销其他会话，保留当前浏览器。
- GET `/users?q=&page=1&page_size=20` → `{items:User[],total,page,page_size}`，仅管理员。
- POST `/users` `{username,password,display_name?,node_limit?,source_limit?,subscription_limit?}` → User，201，仅管理员。
- PATCH `/users/{id}` `{display_name?,password?,enabled?,node_limit?,source_limit?,subscription_limit?}` → User，仅管理员。

用户名支持 1–64 位字母、数字、点、下划线和横线；密码为 8–1024 字节。管理员 ID=1，初次迁移沿用 `ADMIN_PASSWORD` 或 `DATA_DIR/admin-password`；数据库内管理员初始化后，环境变量不会覆盖后来设置的密码。密码使用 SHA-256 预散列后 bcrypt 存储。

普通用户默认额度为账号合计 200 个唯一节点、3 个不同来源、1 条订阅。额度须为正整数，管理员逐用户调整。停用账号立即撤销会话和原订阅令牌，重新启用不会恢复旧令牌；管理员不能被停用。

## 系统与权限

GET `/system`：管理员返回 `{name,version,commit,build_time,database:{configured,connected},core:{state,version},tasks:{pending_sources,pending_nodes,active_sources,active_nodes}}`；普通用户仅返回 `{name,version}`。

全局设置、用户管理、批量检测、Release 查询以及更新／回退／重启仅管理员可用。普通用户只能管理本人来源，手动检测本人来源关联的节点。

## 来源与提供方

`Source = {id,owner_id,provider_name,max_users,claimed_users,name,kind,enabled,exclude_pattern,status:"queued"|"processing"|"ready"|"error",node_count,excluded_count,last_error,last_refresh_at,next_refresh_at,created_at}`。

- GET `/sources` → `{items:Source[]}`；普通用户仅本人，管理员全部。
- POST `/sources` `{name,kind:"url"|"file"|"links",url?,content?,exclude_pattern?,max_users?}` → Source，202。URL 来源传 url，其余传 content。
- PATCH `/sources/{id}` `{name?,enabled?,exclude_pattern?,url?,max_users?}` → Source；空 url 保留已有地址，排除规则变化会重新处理缓存内容。
- POST `/sources/{id}/refresh` → `{queued:true}`；URL 来源重新抓取，文件和链接重新解析已保存内容。
- DELETE `/sources/{id}` → `{deleted:true}`。
- GET `/sources/catalog?q=&provider_id=&following=1&page=1&page_size=20` → `{items:Source[],total,page,page_size}`；共享目录只列出已启用提供方的已启用来源，且须包含未排除、未屏蔽节点。
- GET `/providers?q=&following=1&page=1&page_size=20` → `{items:Provider[],total,page,page_size}`。
- POST／DELETE `/providers/{id}/follow` → `{followed:boolean}`。

`Provider = {id,username,display_name,source_count,node_count,followers,followed,provider_path}`。提供方入口为 `/nodes?provider_id={id}`，浏览器登录后选择其节点。关注不占领取名额。

`max_users=0` 表示不限。领取人数为 `(source_id,user_id)` 历史唯一账号数：同账号领取多个节点、多个组合及客户端刷新只计一次；保存事务成功才占名额，失败不占。删除组合和取消关注不回收历史名额，降低人数上限不会踢出已领取账号。来源 URL 唯一性按 owner_id＋URL 哈希计算；节点仍按完整有效配置全局去重。管理列表和共享目录不返回来源 URL、缓存内容或节点凭据。

## 节点

GET `/nodes?page=1&page_size=50&q=&type=&countries=JP,US&source_id=&provider_id=&following=1&status=&sort=latency|name|created` → `{items:Node[],total,page,page_size,stats:{total,healthy,unhealthy,pending}}`。

兼容旧 `country` 和 `max_latency` API 参数；网页已移除最大延迟筛选和相关 URL 状态。

`Node = {id,name,type,server,port,latency_ms:number|null,status:"unknown"|"healthy"|"unhealthy",country,country_code,region,city,exit_ip,last_checked_at,next_check_at,last_error,sources:[{id,name,node_name,owner_id,provider_name}]}`。

- POST `/nodes/{id}/test` → `{queued:true}`；管理员或本人来源关联节点。
- POST `/nodes/test` `{ids?:number[]}` → `{queued:true}`，仅管理员；省略 ids 检测全部有效节点。
- GET `/options` → `{types:string[],countries:[{code,name}]}`。

检测失败按 1／2／4／8／16／24 小时退避，之后最多 24 小时；连续 7 天无成功检测则屏蔽。屏蔽节点不计入正常列表、统计、选项或无 ID 的批量检测。明确 ID 的手动复测仍可恢复屏蔽节点，成功后清零失败并恢复 6 小时错峰。`blocked`、`last_success_at` 为内部字段。

## 组合订阅

`Subscription = {id,name,node_count,source_count,created_at,updated_at,url,nodes:[{node_id,source_id}],node_ids:number[],source_ids:number[]}`。`url` 是相对地址 `/sub/{token}`，前端复制时补当前站点源。

- GET `/subscriptions` → `{items:Subscription[]}`，仅本人。
- POST `/subscriptions` `{name,nodes:[{node_id,source_id}],source_ids:number[]}` → `{subscription,truncated,selected_count}`，201。
- PUT `/subscriptions/{id}` 同输入／输出，200，仅本人。
- DELETE `/subscriptions/{id}` → `{deleted:true}`，原链接立即失效。
- POST `/subscriptions/{id}/rotate` → Subscription，旧令牌立即失效。

显式节点须附真实来源归属；保存按账号全部组合合计去重计额，事务锁定账号及来源以防并发超领。非空 `source_ids` 表示主动选择整个来源，按来源和节点 ID 升序填入剩余额度，超出部分截断并返回提示。输出 `source_ids` 是实际选中节点的来源集合。**原样编辑快照时提交已有 `nodes`，并令 `source_ids:[]`**；把响应 source_ids 直接回传会重新进行整来源选择。

保存后固定节点快照。输出过滤已删除关系、排除／屏蔽节点、停用来源及停用提供方，不补入用户未选择的新节点；同时按最新账号额度重新裁切。降低订阅条数额度时，按已有订阅顺序限制可用输出。

## 客户端下载

GET `/sub/{token}?format=clash|v2ray|auto`，**无 `/api/v1` 前缀**，无 Cookie。

- `clash`：包含完整代理配置、选择组和规则的 Clash／Mihomo YAML。
- `v2ray`：UTF-8 分享 URI 按行连接后 Base64。支持可以完整表达的 SS／SS2022、VMess、VLESS、Trojan、Hysteria2、TUIC、HTTP／SOCKS；不能保留参数的节点会跳过，全部无法表达则 422。
- `auto` 或省略：按 User-Agent 选择。Mihomo／Clash／Stash 返回 Clash，v2rayN／v2rayNG 等兼容客户端返回 URI 订阅，未知 UA 默认 Clash。显式格式优先。

URI 订阅只承诺兼容 URI 导入；不提供 sing-box JSON，不把 URI 集合伪装为 JSON。复杂插件、传输头、证书选项等无法保真时，请使用 Clash 格式。

响应提供 `X-Subscription-Format`、`X-Subscription-Skipped`；v2ray 另带 `X-Subscription-Compatibility: v2ray-compatible URI import only`。设置合理 Content-Type、inline 文件名、`Cache-Control: no-store, private, max-age=0`、`Vary: User-Agent` 与 `Referrer-Policy: no-referrer`。

## 设置和版本

- GET `/settings` → `{exclude_pattern,test_url,timeout_ms,probe_concurrency,source_concurrency,refresh_hours:6,check_hours:6}`。
- PUT `/settings` `{exclude_pattern,test_url,timeout_ms,probe_concurrency,source_concurrency}` → 同上。先校验正则，再按缓存内容重算，失败保留旧列表。
- GET `/releases` → `{current_version,latest_version,update_available,can_update,can_rollback,need_restart,state,message,releases:[{version,name,body,published_at,url}]}`。
- POST `/system/update` `{version?:string}` → `{queued:true}`，默认最新稳定版。
- POST `/system/rollback` `{version?:string}` → `{queued:true}`，空值恢复本地 backup，否则安装指定历史版。
- POST `/system/restart` → `{restarting:true}`，Linux 退出后由 Docker restart 策略拉起。

这些接口仅管理员可用。更新／回退异步执行，通过 `/releases` 轮询。回退只处理程序文件，不处理数据库。

## 网页路由与外观

- `/nodes`：节点页，支持分页、协议、可搜索分页来源、国旗网格地区多选、状态、排序、提供方及关注范围。
- `/sources`：本人来源管理，管理员可管理全部；`q` 保存来源名称搜索。
- `/providers`：提供方目录，`q`、`following`、`page` 可还原。
- `/subscriptions`、`/account`：个人组合订阅和账号额度／设置。
- `/users`、`/settings`：管理员页面。

默认卡片；`page=1`、`page_size=50`、`sort=latency`、`view=cards` 从 URL 省略。离散筛选、分页和视图切换保留历史，搜索输入替换历史。节点选区在当前登录会话内跨页保留，退出或账号失效时清空。

首次跟随系统亮暗，太阳／月亮图标切换后保存显式偏好。版本入口显示后端真实版本文字；普通用户仅查看版本，管理员打开更新弹窗。顶栏不显示正常数据库连接说明，异常仍有明确提示。
