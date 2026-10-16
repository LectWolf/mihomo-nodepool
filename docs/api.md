# 第一版 API 契约

所有 API 返回 JSON，错误为 `{ "error": "中文说明" }`。业务 API 需要登录 Cookie；数据库不可用返回 503。第一版仅单管理员，暂不开注册。

## 登录

- GET `/api/v1/auth` -> `{authenticated:boolean,username:"admin"}`。
- POST `/api/v1/auth/login` `{password:string}` -> `{authenticated:true}` 并设置 Cookie。
- POST `/api/v1/auth/logout` -> `{authenticated:false}`。
- 管理员密码通过 ADMIN_PASSWORD 提供；未设置时生成并保存在 DATA_DIR/admin-password，启动日志只提示路径。

## 系统

GET `/api/v1/system` -> `{name,version,commit,build_time,database:{configured,connected},core:{state:"ready"|"error",version:string},tasks:{pending_sources:number,pending_nodes:number,active_sources:number,active_nodes:number}}`。

## 来源

- GET `/api/v1/sources` -> `{items:Source[]}`。
- POST `/api/v1/sources` `{name:string,kind:"url"|"file"|"links",url?:string,content?:string,exclude_pattern?:string}` -> Source，202。URL 来源传 url，其余传 content；返回时不泄露 URL 令牌或节点原配置。
- PATCH `/api/v1/sources/{id}` `{name?:string,enabled?:boolean,exclude_pattern?:string,url?:string}` -> Source。空 url 保留已有 URL；规则变化触发重新处理缓存内容。
- POST `/api/v1/sources/{id}/refresh` -> `{queued:true}`。重新抓取 URL 来源；文件和链接重新解析已保存内容。
- DELETE `/api/v1/sources/{id}` -> `{deleted:true}`。
- 没有刷新所有来源的操作。

Source: `{id,name,kind,enabled,exclude_pattern,status:"queued"|"processing"|"ready"|"error",node_count,excluded_count,last_error,last_refresh_at:string|null,next_refresh_at:string|null,created_at:string}`。

## 节点

GET `/api/v1/nodes?page=1&page_size=50&q=&type=&country=&source_id=&status=&sort=latency|name|created&max_latency=` -> `{items:Node[],total,page,page_size,stats:{total,healthy,unhealthy,pending}}`。
Node: `{id,name,type,server,port,latency_ms:number|null,status:"unknown"|"healthy"|"unhealthy",country,country_code,region,city,exit_ip,last_checked_at:string|null,next_check_at:string,last_error,sources:[{id,name,node_name}]}`。地区字段可以为空；前端按国家、省／州、城市组合展示。
- POST `/api/v1/nodes/{id}/test` -> `{queued:true}`。
- POST `/api/v1/nodes/test` `{ids?:number[]}` -> `{queued:true}`。省略 ids 测全部有效节点，手动请求仍排队、去重和限并发。
- GET `/api/v1/options` -> `{types:string[],countries:[{code,name}]}`。
- 节点连接检测失败使用 1/2/4/8/16/24 小时退避，之后最多 24 小时；连续 7 天无成功检测则屏蔽。屏蔽节点不计入有效列表、统计、选项或无 ID 的批量检测。
- 指定 ID 的检测允许重新检测仍有有效来源的屏蔽节点；成功即恢复健康、解除屏蔽并恢复 6 小时错峰。`blocked`、`last_success_at` 为内部字段，不添加到节点响应。

## 设置

GET `/api/v1/settings` -> `{exclude_pattern,test_url,timeout_ms,probe_concurrency,source_concurrency,refresh_hours:6,check_hours:6}`。
PUT `/api/v1/settings` `{exclude_pattern,test_url,timeout_ms,probe_concurrency,source_concurrency}` -> 同上。正则先校验；全局规则变化使来源按缓存内容重算，失败保留原列表。

## 版本

- GET `/api/v1/releases` -> `{current_version,latest_version,update_available,can_update,can_rollback,need_restart,state:"idle"|"checking"|"downloading"|"installing"|"restarting"|"done"|"error",message,releases:[{version,name,body,published_at,url}]}`。
- POST `/api/v1/system/update` `{version?:string}` -> `{queued:true}`。默认最新稳定版。
- POST `/api/v1/system/rollback` `{version?:string}` -> `{queued:true}`。空恢复本地 backup，否则下载指定历史版。
- POST `/api/v1/system/restart` -> `{restarting:true}`。Linux 进程退出，由 Docker restart 策略拉起；非 Linux 返回说明性错误。
- 更新、回退异步执行，通过 GET releases 的 state 轮询。回退不处理数据库。

## 网页路由与外观

- `/nodes`：节点页，查询参数支持 `page`、`page_size`、`view=list|cards` 和节点 API 的筛选／排序参数。
- 默认卡片；`page=1`、`page_size=50`、`sort=latency`、`view=cards` 从网页地址省略。`view=list` 明确表示列表，刷新和前进后退按 URL 还原。最大延迟与搜索输入替换当前历史，其余筛选、排序、分页和视图切换保留历史。
- `/sources`：来源页，`q` 保存来源名称搜索。
- `/settings`：运行设置。
- 页面切换、节点翻页和离散筛选进入浏览器历史；地址可直接打开并在登录后还原。输入搜索词时使用替换历史，避免每个字符产生一条历史。
- 首次访问跟随系统亮暗状态，浏览器本地保存 `system|light|dark` 外观选择。
