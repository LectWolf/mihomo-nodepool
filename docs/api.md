# API 契约

业务 API 使用 `/api/v1` 前缀并返回 JSON，错误为 `{ "error": "中文说明" }`。除认证状态、登录及健康检查外，业务 API 需要登录 Cookie；客户端订阅使用独立 `/sub/{token}` 入口，无需 Cookie。暂不开放自助注册。

常用错误：400 输入无效，401 未登录或会话失效，403 越权，404 对象或令牌不存在，409 额度／领取名额冲突，422 无可导出节点，503 数据库不可用。

## 账号与认证

`User = {id,username,display_name,role:"admin"|"user",enabled,node_limit,source_limit,subscription_limit,created_at,xp,points,level,checkin_on,checkin_streak,subscription_expires_at}`。经验／积分0–1e9，管理员PATCH支持`xp,points`；个人PATCH不支持。等级由经验自动计算。

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

- POST `/me/checkin` `{confirm_partial:boolean}` → `{user,awarded,xp_added,points_added,needs_confirmation,skipped:[{source_id,name,reason}]}`。北京时间每日5XP／2积分，每连签7日额外20XP／8积分；同日幂等但仍续租。
- GET `/levels` → `{total,counts:[7],thresholds:[0,50,200,1000,3500,10000,30000],names:["初至","识途","通行","常客","熟路","向导","彼岸"]}`，统计启用普通账号。

默认个人14天有效期，普通拉取不续租。部分来源无法激活时`needs_confirmation=true`仅返回预览，不发奖励、不变更快照；确认后排除失败来源并激活其余，保留原链接。满额、停用、禁领、等级不足分别提供原因。

## 系统与权限

GET `/system`：管理员返回 `{name,version,commit,build_time,database:{configured,connected},core:{state,version},tasks:{pending_sources,pending_nodes,active_sources,active_nodes}}`；普通用户仅返回 `{name,version}`。

全局设置、用户管理、所有手动检测、Release 查询以及更新／回退／重启仅管理员可用。普通用户只能管理本人来源；检测按钮仅出现在管理员的节点详情内。

## 来源与提供方

`Source = {id,owner_id,provider_name,max_users,claimed_users,active_users,occupied_users,min_level,share_url?,subscription_usage?,name,kind,enabled,exclude_pattern,status:"queued"|"processing"|"ready"|"error",node_count,excluded_count,last_error,last_refresh_at,next_refresh_at,created_at}`。`share_url`仅管理列表返回，公共目录不披露。

- GET `/sources` → `{items:Source[]}`；普通用户仅本人，管理员全部。
- POST `/sources` `{name,kind:"url"|"file"|"links",url?,content?,exclude_pattern?,max_users?,min_level?}` → Source，202。URL来源传url，其余传content。
- PATCH `/sources/{id}` `{name?,enabled?,exclude_pattern?,url?,max_users?,min_level?}` → Source；空url保留已有地址，排除变化重新处理缓存。最低等级0–6，默认0不限。
- POST `/sources/{id}/refresh` → `{queued:true}`；URL 来源重新抓取，文件和链接重新解析已保存内容。
- DELETE `/sources/{id}` → `{deleted:true}`。
- GET `/sources/catalog?q=&provider_id=&following=1&page=1&page_size=20` → `{items:Source[],total,page,page_size}`；共享目录只列出已启用提供方的已启用来源，且须包含未排除、未屏蔽节点。
- GET `/providers?q=&following=1&page=1&page_size=20` → `{items:Provider[],total,page,page_size}`。
- POST／DELETE `/providers/{id}/follow` → `{followed:boolean}`。

`Provider = {id,username,display_name,source_count,node_count,followers,followed,provider_path}`。提供方入口为 `/nodes?provider_id={id}`，浏览器登录后选择其节点。关注不占领取名额。

`max_users=0`不限。容量统计`np_source_seats`中启用账号未过期的有效席位，同账号多个节点／组合计一人。删除最后引用、踢出或过期释放；历史领取不占容量，降低上限保留有效已有席位。`active_users`与`occupied_users`均表示当前有效席位人数，`claimed_users`表示保留的历史领取账号数。来源URL唯一性按owner＋URL哈希，节点按完整配置去重；管理列表与共享目录不返回上游URL、缓存内容或节点凭据。

- GET `/sources/{id}/subscribers?state=current|history|all&page=1&page_size=20` → `{items,total,page,page_size,history_retention_days}`，仅owner／admin。Item为`{user_id,username,display_name,level,xp,enabled,state,claimed_at,expires_at,last_pull_at,last_renewed_at,renew_method,ended_at,node_count,subscription_count}`。
- PATCH `/sources/{id}/subscribers/{userID}` `{blocked:boolean}` → `{blocked}`。踢出从全部组合移除本来源并禁领，直到明确解除；owner自身不可踢。禁领不随历史清理失效。
- POST `/sources/{id}/link/rotate` → `{url:"/link/{token}"}`，仅owner／admin，旧直链失效。
- GET `/link/{token}?format=clash|v2ray|auto` 无API前缀和Cookie，下载整个来源，绕过个人席位／等级，不附个人保活；来源和owner必须启用。持有链接即可访问。

## 节点

GET `/nodes?page=1&page_size=50&q=&type=&countries=JP,US&min_level=0..6&source_id=&provider_id=&following=1&status=&sort=latency|name|created` → `{items:Node[],total,page,page_size,stats:{total,healthy,unhealthy,pending}}`。`min_level`精确匹配来源门槛，有多个来源时任一匹配即可。

兼容旧 `country` 和 `max_latency` API 参数；网页已移除最大延迟筛选和相关 URL 状态。

`Node = {id,name,type,server,port,latency_ms:number|null,status:"unknown"|"healthy"|"unhealthy",country,country_code,region,city,exit_ip,last_checked_at,next_check_at,last_error,sources:[{id,name,node_name,owner_id,provider_name}]}`。

Node另带`insights:{v2ray_compatible,v2ray_reason,cipher?,transport?,tls,warnings:[{code,severity,message}]}`，Origin另带`min_level,subscription_usage?`；只分析配置，不保证实际客户端连通，不输出凭据。TLS状态按协议判断，不把SS AEAD／SS2022／WireGuard泛化为明文。

`SubscriptionUsage={upload_bytes,download_bytes,total_bytes,remaining_bytes,expires_at,status,fetched_at}`，缺失值为null，未提供头则整个对象省略；来源级元数据，不是个人用量。`status=unknown|normal|low|exhausted|expired|invalid`，low为已知剩余≤10%；total=0表示未知，整数／重复／溢出严格校验。到期时间为UTC ISO，读取时刷新到期状态。

- POST `/nodes/{id}/test` → `{queued:true}`，仅管理员。
- POST `/nodes/test` `{ids?:number[]}` → `{queued:true}`，仅管理员；省略 ids 检测全部有效节点。
- GET `/options` → `{types:string[],countries:[{code,name}]}`。

检测失败按 1／2／4／8／16／24 小时退避，之后最多 24 小时；连续 7 天无成功检测则屏蔽。屏蔽节点不计入正常列表、统计、选项或无 ID 的批量检测。明确 ID 的手动复测仍可恢复屏蔽节点，成功后清零失败并恢复 6 小时错峰。`blocked`、`last_success_at` 为内部字段。

## 组合订阅

`Subscription = {id,name,node_count,source_count,created_at,updated_at,url,nodes:[{node_id,source_id}],node_ids:number[],source_ids:number[],rule_profile_id:number|null}`。`url` 是相对地址 `/sub/{token}`，前端复制时补当前站点源。

- GET `/subscriptions` → `{items:Subscription[]}`，仅本人。

GET列表另带`node_details:[{node_id,source_id,name,source_name,type,server,port,available,insights,min_level,subscription_usage?}]`，不含凭据。保存只提交ID对，关系删除后保留ID并标为不可用。

- POST `/subscriptions` `{name,nodes:[{node_id,source_id}],source_ids:number[],rule_profile_id?:number|null}` → `{subscription,truncated,selected_count}`，201。
- PUT `/subscriptions/{id}` 同输入／输出，200，仅本人。
- PATCH `/subscriptions/{id}` `{rule_profile_id:number|null}` → Subscription，200；仅更新规则绑定，不重新校验或领取历史快照节点。即使来源已停用／删除也可切换或解绑，保留节点清单及令牌。
- DELETE `/subscriptions/{id}` → `{deleted:true}`，原链接立即失效。
- POST `/subscriptions/{id}/rotate` → Subscription，旧令牌立即失效。

显式节点须附真实来源归属；保存按账号全部组合合计去重计额，事务锁定账号及来源以防并发超领。非空 `source_ids` 表示主动选择整个来源，按来源和节点 ID 升序填入剩余额度，超出部分截断并返回提示。输出 `source_ids` 是实际选中节点的来源集合。**原样编辑快照时提交已有 `nodes`，并令 `source_ids:[]`**；把响应 source_ids 直接回传会重新进行整来源选择。

保存后固定节点快照。输出过滤已删除关系、排除／屏蔽节点、停用来源及停用提供方，不补入用户未选择的新节点；同时按最新账号额度重新裁切。降低订阅条数额度时，按已有订阅顺序限制可用输出。

PUT 覆盖会先排除目标组合的旧用量再校验账号合计额度，保留原令牌。网页保存弹窗可选择新建或覆盖，订阅数满额时仍可选节点并覆盖。`rule_profile_id=0` 绑定官方默认规则，正整数仅可绑定本人私人规则，显式 `null` 不绑定。新建省略默认 0；编辑省略保留原值，旧 null 不强制迁移；负数或错误类型返回 400。PATCH 必须显式提供该字段。

- GET `/subscriptions/history?page=&page_size=` → `{items:[{source_id,source_name,action,created_at}],total,page,page_size,history_retention_days}`，本人来源领取／释放等事件。
- GET `/subscriptions/archives?page=&page_size=` → 同分页结构，items为`{id,subscription_id,name,nodes,source_ids,node_details,rule_profile_id,reason,archived_at}`。reason为`replaced|deleted|source_removed`；显示快照不含token／url／连接凭据，不可恢复旧下载资格。

历史默认30天，查询按保留窗口过滤并每分钟清理，禁领独立保留。过期个人账号不能新增／覆盖组合，应先网页激活。

## 规则组合

`RuleProfile = {id,name,official:boolean,readonly:boolean,routing_enabled,dns_enabled,dns_servers:string[],rules:string[],created_at,updated_at}`。官方内置项 ID 为 0，`official/readonly` 均 true，所有账号直接可用，无需创建数据库副本；正整数为账号私有组合，两标记为 false。

- GET `/rule-profiles` → `{items:RuleProfile[]}`，官方项始终排第一，其后仅本人私人组合。
- GET `/rule-profiles/default` → 完整官方模板，与列表中 ID 0 内容一致。
- POST `/rule-profiles` 同上配置字段 → RuleProfile，201。
- PUT `/rule-profiles/{id}` 同上配置字段 → RuleProfile。
- DELETE `/rule-profiles/{id}` → `{deleted:true}`；仍被订阅绑定时 409，须先解绑。

PUT／DELETE `/rule-profiles/0` 返回 403，官方规则不可修改或删除。复制通过 POST 提交配置字段 `name,routing_enabled,dns_enabled,dns_servers,rules` 创建私人项；响应元数据不属于写入字段。

配置字段另含`groups:RuleGroup[]`，`RuleGroup={name,type,include_all,node_ids,countries,groups,interval,tolerance}`。type为select／url-test／fallback／load-balance；最多64组，必须含NodePool，名称唯一≤100字符、不含控制字符／逗号、不能用DIRECT／REJECT／GLOBAL；每组最多2000正节点ID／64国家／64引用。interval默认300、60–86400秒；tolerance默认50、0–1000毫秒。旧NULL／省略维持默认七组。引用DIRECT／REJECT或其他组，禁止悬空、自环和循环。指定节点与国家取并集，再追加组引用；只在本次成功导出的已选节点中匹配，不额外领取。HK／TW／MO独立，CF按现有节点分类视为Cloudflare而非国家；空组DIRECT回退。

默认分流沿用本机“订阅共享”的完整规则表，主代理组名统一为 `NodePool`，包含自动选择、故障转移、Cloudflare、国内网站、拦截广告和漏网之鱼。规则按序匹配，可编辑；不加入品牌或流量信息假节点。默认 DNS 开启且留空时写入 `223.5.5.5`、`119.29.29.29`、`8.8.8.8`。关闭 DNS 则不写入 DNS 段。

仅 Clash 应用规则组合，v2ray 导出保持节点 URI。未绑定规则组合的旧订阅维持现有仅节点配置。关闭分流则从已缓存来源配置合并可保留的本地规则／策略组，按来源稳定排序并区分同名引用，只包含本次选中节点；无来源规则时使用 `MATCH,NodePool` 兜底。

自定义规则支持 `DOMAIN`、`DOMAIN-SUFFIX`、`DOMAIN-KEYWORD`、`IP-CIDR`、`IP-CIDR6`、`DST-PORT`、`SRC-PORT`、`NETWORK`、`GEOIP(CN/LAN)`、`MATCH`，IP／GEOIP 支持 `no-resolve`；目标限定为上述策略组、DIRECT 和 REJECT。最多 20,000 条、每条 4,096 字节、合计 2 MiB。DNS 最多 32 项，支持 IP 或 TLS／HTTPS／UDP／TCP 地址。

跟随来源时保留 `select/url-test/fallback/load-balance` 的本地已选成员，采用安全默认组参数及固定 gstatic 探测地址。外部 `proxy-providers`、`rule-providers`、`use`、`RULE-SET` 不透传；不支持的规则、空组、无效引用计入 `X-Subscription-Routing-Skipped` 响应头。多来源具体规则按来源 ID／内部顺序匹配，末尾使用最小来源 ID 的首个有效 MATCH。节点 `dialer-proxy` 依赖须在本来源已选节点中，缺失或循环返回 422，避免输出失效配置。

## 客户端下载

GET `/sub/{token}?format=clash|v2ray|auto`，**无 `/api/v1` 前缀**，无 Cookie。

- `clash`：包含完整代理配置、选择组和规则的 Clash／Mihomo YAML。
- `v2ray`：UTF-8 分享 URI 按行连接后 Base64。支持可以完整表达的 SS／SS2022、VMess、VLESS、Trojan、Hysteria2、TUIC、HTTP／SOCKS；不能保留参数的节点会跳过，全部无法表达则 422。
- `auto` 或省略：按 User-Agent 选择。Mihomo／Clash／Stash 返回 Clash，v2rayN／v2rayNG 等兼容客户端返回 URI 订阅，未知 UA 默认 Clash。显式格式优先。

URI 订阅只承诺兼容 URI 导入；不提供 sing-box JSON，不把 URI 集合伪装为 JSON。复杂插件、传输头、证书选项等无法保真时，请使用 Clash 格式。

响应提供 `X-Subscription-Format`、`X-Subscription-Skipped`、`X-Subscription-Routing-Skipped`；v2ray 另带 `X-Subscription-Compatibility: v2ray-compatible URI import only`。设置合理 Content-Type、inline 文件名、`Cache-Control: no-store, private, max-age=0`、`Vary: User-Agent` 与 `Referrer-Policy: no-referrer`。

个人下载另带`X-Subscription-State:active|expired`、`Profile-Web-Page-Url`和`Subscription-Userinfo: expire=个人租期Unix秒`。到期返回200提示节点，不输出真实节点。有效下载附保活VLESS/WS节点及独立hidden url-test组；只有固定HTTP gstatic204的HEAD／GET有效探测续期，旧过期UUID不能复活。GET `/ws/lease/{uuid}`仅WebSocket协议入口，不提供通用转发。公网源优先settings.public_url，否则按请求TLS／Host推断，不信任转发头；反代须配置真实HTTPS源和WS Upgrade。来源`/link/`转发已解析来源用量头，不套个人到期。

## 设置和版本

- GET `/settings` → `{exclude_pattern,test_url,timeout_ms,probe_concurrency,source_concurrency,refresh_hours:6,check_hours:6,subscription_lease_days:14,history_retention_days:30,public_url:""}`。
- PUT `/settings`提交以上可编辑字段，固定周期不需提交。租期1–365天、历史1–3650天，旧缺字段使用默认值；public_url可空，否则必须无用户信息／路径／查询的HTTP(S)源地址。排除正则变化按缓存重算，失败保留旧列表。
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
- `/rules`：查看官方默认规则，复制为私人组合；私人工作台支持搜索、每页30条、逐条增改、上下移动、文本编辑、DNS分区和撤销本次修改。
- `/users`、`/settings`：管理员页面。

默认卡片；`page=1`、`page_size=50`、`sort=latency`、`view=cards` 从 URL 省略。离散筛选、分页和视图切换保留历史，搜索输入替换历史。节点选区在当前登录会话内跨页保留，退出或账号失效时清空。

节点默认整卡点击查看详情，进入选择模式后整卡选择，支持键盘操作。卡片单行标题省略并提供悬浮全文。桌面地区面板每行 5 项，名称一行、内部滚动；整来源弹窗并列显示当前选区与来源的选择状态。

地区选中项带勾选标记，面板显示已选数量和可取消标签，触发器显示已选名称；“完成”关闭后恢复焦点。当前选区按真实来源名称分组，统一“移除”控件。弹窗遮罩关闭要求按下与松开都在外部、移动不超过6px；内容拖出、外部拖入、内部留白均不误关，Escape与关闭按钮仍可用。

首次跟随系统亮暗，太阳／月亮图标切换后保存显式偏好。版本入口显示后端真实版本文字；普通用户仅查看版本，管理员打开更新弹窗。顶栏不显示正常数据库连接说明，异常仍有明确提示。
