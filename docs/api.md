# API 契约

业务 API 使用 `/api/v1` 前缀并返回 JSON，错误为 `{ "error": "中文说明" }`。除认证状态、登录及健康检查外，业务 API 需要登录 Cookie；客户端订阅使用独立 `/sub/{token}` 入口，无需 Cookie。暂不开放自助注册。

常用错误：400 输入无效，401 未登录或会话失效，403 越权，404 对象或令牌不存在，409 额度／领取名额冲突，422 无可导出节点，503 数据库不可用；维护模式拦截写操作时返回 503 `{error,maintenance:true}`。

## 账号与认证

`User = {id,username,display_name,role:"admin"|"manager"|"user",enabled,node_limit,source_limit,subscription_limit,created_at,xp,points,level,checkin_on,checkin_streak,subscription_expires_at,email,email_verified}`。经验／积分0–1e9，管理员PATCH支持`xp,points`；个人PATCH不支持。等级由经验自动计算。

- GET `/auth` → `{authenticated,username,user:User|null,persistent,guest_browse,registration,login_options:{email,github,linuxdo,require_email,email_suffixes},email_required}`。`login_options.email` 表示 SMTP 已配置；`email_required=true` 时前端弹出不可关闭的“绑定邮箱”窗口。
- POST `/auth/email/code` `{email,purpose:"register"|"login"|"reset"}` → `{ok,cooldown:60}`。验证码 6 位、10 分钟有效、最多试错 5 次；同邮箱同用途 60 秒一次（429），每 IP 每小时 10 次。注册用途校验允许的邮箱后缀（400）和是否已注册（409）；登录与找回密码不暴露邮箱是否存在。
- POST `/auth/register` `{username,email?,code?,password,display_name?,remember?}` → 同 `/auth/login`，创建 Lv0 普通账号并直接登录。配置 SMTP 后必须提供已验证的邮箱和验证码；用户名 3–20 位、字母开头、仅字母数字 `_` `-`，注册后不可修改。重名 409，关闭 403，维护模式 503；每 IP 每小时 5 次、全站每小时 60 次，超出 429。
- POST `/auth/login/code` `{email,code,remember?}` → 邮箱验证码登录。POST `/auth/password/reset` `{email,code,password}` → `{ok}`，重置后撤销全部会话。
- GET `/auth/oauth/{github|linuxdo}/start?mode=login|bind&remember=1` → 302 到第三方授权页；state 只在服务端保存 10 分钟，并以 SameSite=Lax 的 `nodepool_oauth` Cookie 绑定浏览器。回调 `/auth/oauth/{provider}/callback`：已绑定则登录并跳转 `/?oauth={provider}`；未绑定且开放注册时自动创建账号（用户名取第三方登录名，冲突追加数字），跳转 `/?oauth={provider}&welcome=1`，之后需补齐邮箱；`mode=bind` 成功跳转 `/account?bound={provider}`。失败统一跳转并带 `oauth_error`。回调地址为 `{public_url}/api/v1/auth/oauth/{provider}/callback`。
- POST `/me/email/code` `{email}` → 向新邮箱发送绑定验证码（校验后缀和占用）；PUT `/me/email` `{email,code}` → User，绑定或换绑。
- GET `/me/identities` → `{items:[{provider,login,created_at}],providers:{github,linuxdo}}`；DELETE `/me/identities/{provider}` 解绑。
- 邮件服务可用且 `login.require_email=true` 时，没有邮箱的非管理员账号只能浏览（GET）、绑定邮箱、修改昵称／密码／头像和退出，其他写操作返回 403 `{error,email_required:true}`。
- POST `/auth/login` `{username?,password,remember?}` → 同上，并设置 HttpOnly、SameSite Cookie；`username` 可以是用户名或已绑定邮箱，省略用户名兼容 `admin`。账号存在但没有设置密码（旧平台迁移、第三方注册）时返回 409 `{error,password_unset:true,email_hint,providers}`：`email_hint` 是打码后的已验证邮箱（邮件服务未配置时为空），`providers` 是已绑定且已启用的第三方登录；前端引导用邮箱验证码设置密码，或经第三方登录后强制补设密码。默认是浏览器会话 Cookie（服务端 12 小时）；`remember=true` 时 Cookie 与服务端会话为 30 天，使用中剩余不足 29 天会自动续期。
- 登录失败按出口 IP（30 次/10 分钟）与「出口 IP + 账号」（8 次/15 分钟）分别计数，超限返回 429 与 `Retry-After`，成功后清零该组合；不设全站计数器，单个出口的失败不影响其他 IP 或其他账号。出口 IP 只在直连地址属于 `NODEPOOL_TRUSTED_PROXIES` 时取自 `X-Forwarded-For`。
- POST `/auth/persist` `{remember}` → 同 `/auth`；登录后“保持登录状态？”确认用，把当前会话改为 30 天持久会话。会话以 SHA-256 摘要存入 `np_sessions`，服务重启不掉线，退出登录、改密码或停用账号即失效。
- POST `/auth/logout` → `{authenticated:false}`。
- GET `/me` → `{user:User,usage:{nodes,sources,subscriptions},provider_path}`。
- PATCH `/me` `{display_name?,password?,current_password?,beacon_enabled?}` → User；User 含 `has_password`、`beacon_enabled`（实测回报，默认开启，关闭后客户端下次更新订阅生效）。已有密码的账号修改密码必须提供正确的 `current_password`（错误 400，同一账号 15 分钟内错 5 次后 429）；没有密码的账号可直接设置。修改密码撤销其他会话，保留当前浏览器。
- GET `/users?q=&role=&status=&risk=&quota=&page=1&page_size=20` → `{items:User[],total,page,page_size,limits,risk}`，管理员与用户管理员（manager）。`role=admin|manager|user`，`status=enabled|disabled`，`risk=flagged|frozen|clean`（触发任一风控标记／冻结中／无标记），`quota=custom|level`（单独设置额度／跟随等级）；参数可组合。
- POST `/users` `{username,password,display_name?,node_limit?,source_limit?,subscription_limit?,role?}` → User，201。管理员创建的账号沿用 1–64 位用户名规则，便于导入旧平台数据。
- PATCH `/users/{id}` `{display_name?,password?,enabled?,node_limit?,source_limit?,subscription_limit?,xp?,points?,role?}` → User。

角色：`admin` 拥有全部权限；`manager`（用户管理员）只能创建和管理 `role=user` 的账号（含风控操作），不能设置角色，也不能访问运行设置、运行统计、发布与更新；`user` 为普通账号。只有管理员能设置角色，初始管理员（ID=1）与自己的角色不可修改。

用户名支持 1–64 位字母、数字、点、下划线和横线；密码为 8–1024 字节。管理员 ID=1，初次迁移沿用 `ADMIN_PASSWORD` 或 `DATA_DIR/admin-password`；数据库内管理员初始化后，环境变量不会覆盖后来设置的密码。密码使用 SHA-256 预散列后 bcrypt 存储。

额度默认跟随等级（个人额度字段为 0）；单独设置时节点 1–100000、来源 1–10000、订阅 1–1000，实际额度取等级默认与单独值中较高者再加积分扩容。旧版本默认写入的 200/3/1 会在升级时一次性改为 0（跟随等级）。停用账号立即撤销会话和原订阅令牌，重新启用不会恢复旧令牌；管理员不能被停用。

- POST `/me/checkin` `{confirm_partial:boolean}` → `{user,awarded,xp_added,points_added,needs_confirmation,skipped:[{source_id,name,reason}]}`。北京时间每日5XP／2积分，每连签7日额外20XP／8积分；同日幂等但仍续租。
- GET `/levels` → `{total,counts:[7],thresholds:[0,50,200,1000,3500,10000,30000],names:["初至","识途","通行","常客","熟路","向导","彼岸"]}`，统计启用普通账号。

默认个人14天有效期，普通拉取不续租。部分来源无法激活时`needs_confirmation=true`仅返回预览，不发奖励、不变更快照；确认后排除失败来源并激活其余，保留原链接。满额、停用、禁领、等级不足分别提供原因。

## 系统与权限

GET `/system`：管理员返回 `{name,version,commit,build_time,sampled_at,database:{configured,connected},core:{state,version},tasks:{pending_sources,pending_nodes,active_sources,active_nodes},tasks_error}`；普通用户仅返回 `{name,version}`。`version`、`commit`、`build_time` 为运行程序的构建信息，`commit` 可能带 `-dirty` 或为 `unknown`。`active_*` 为持有有效租约的任务，`pending_*` 为符合领取条件且未被租约占用的任务，两者不重复计数；读取失败时 `tasks_error` 为非空说明，`tasks` 不可作为 0 解读。

GET `/statistics`（仅管理员）→ `{sampled_at,overview,tasks,source_tasks,node_tasks,usage,runtime}`：

- `overview`：`{sources_total,sources_enabled,sources_ready,sources_error,nodes_total,nodes_visible,nodes_healthy,nodes_unhealthy,nodes_unknown,nodes_blocked,users_total,users_enabled,providers_total,subscriptions,active_seats}`。
- `tasks` 同 `/system`；`source_tasks`、`node_tasks` 每类最多 100 条 `{id,name,type,status:processing|queued,lease_until?,next_at?,last_error?}`，执行中优先。队列条件与后台领取一致，包括租约过期恢复及手动复测已屏蔽节点。
- `usage`：`{sources_known,sources_total,upload_bytes,download_bytes,total_bytes,remaining_bytes,upload_sources,download_sources,total_sources,remaining_sources,overflow,items:[{id,name,enabled,usage}]}`。每个来源（含停用）计一次，各字段只累加已知值并给出覆盖来源数；无已知值为 `null`；`invalid` 元数据不计入；累加溢出时该项为 `null` 且 `overflow=true`。同一上游套餐被多个来源导入会重复计数。
- `runtime`：`{started_at,uptime_seconds,heap_bytes,goroutines,source_body_bytes,source_runs,source_errors,probe_runs,probe_errors}`，本次启动后累计，重启归零。`source_body_bytes` 为实际读取的上游订阅响应正文（含失败响应），不含协议开销、检测或出口查询流量；客户端直连节点的流量不经过本服务。

全局设置、运行统计、用户管理、所有手动检测、Release 查询以及更新／回退／重启仅管理员可用。普通用户只能管理本人来源；检测按钮仅出现在管理员的节点详情内。

## 来源与提供方

`Source = {id,owner_id,provider_name,max_users,claimed_users,active_users,occupied_users,min_level,share_url?,subscription_usage?,name,kind,enabled,exclude_pattern,status:"queued"|"processing"|"ready"|"error",node_count,excluded_count,last_error,last_refresh_at,next_refresh_at,created_at}`。`share_url`仅管理列表返回，公共目录不披露。

- GET `/sources?scope=all` → `{items:Source[]}`；默认只返回本人来源，管理员传 `scope=all` 时返回全部（含私有来源）。
- POST `/sources` `{name,kind:"url"|"file"|"links",url?,content?,exclude_pattern?,max_users?,min_level?,private?}` → Source，202。URL来源传url，其余传content。
- PATCH `/sources/{id}` `{name?,enabled?,exclude_pattern?,url?,max_users?,min_level?}` → Source；空url保留已有地址，排除变化重新处理缓存。最低等级0–6，默认0不限。
- POST `/sources/{id}/refresh` → `{queued:true}`；URL 来源重新抓取，文件和链接重新解析已保存内容。
- DELETE `/sources/{id}` → `{deleted:true}`。
- GET `/sources/catalog?q=&provider_id=&following=1&page=1&page_size=20` → `{items:Source[],total,page,page_size}`；共享目录只列出已启用提供方的已启用来源，且须包含未排除、未屏蔽节点。
- GET `/providers?q=&following=1&page=1&page_size=20` → `{items:Provider[],total,page,page_size}`。
- POST／DELETE `/providers/{id}/follow` → `{followed:boolean}`。

`Provider = {id,username,display_name,source_count,node_count,followers,followed,provider_path}`。提供方入口为 `/nodes?provider_id={id}`，浏览器登录后选择其节点。关注不占领取名额。

`max_users=0`不限。容量统计`np_source_seats`中启用账号未过期的有效席位，同账号多个节点／组合计一人。删除最后引用、踢出或过期释放；历史领取不占容量，降低上限保留有效已有席位。`active_users`与`occupied_users`均表示当前有效席位人数，`claimed_users`表示保留的历史领取账号数。来源URL唯一性按owner＋URL哈希，节点按完整配置去重；管理列表与共享目录不返回上游URL、缓存内容或节点凭据。

- GET `/sources/{id}/subscribers?state=current|history|all&page=1&page_size=20` → `{items,total,page,page_size,history_retention_days}`，仅owner／admin。Item为`{user_id,username,display_name,level,xp,enabled,state,claimed_at,expires_at,last_pull_at,last_renewed_at,renew_method,ended_at,node_count,subscription_count}`。
- PATCH `/sources/{id}/subscribers/{userID}` `{action:"remove"|"kick"|"unblock"}` → `{action,blocked}`（旧的 `{blocked:boolean}` 仍可用，true＝kick、false＝unblock）。`remove` 仅踢出：从其全部组合移除本来源、释放有效席位（事件 `removed`），对方之后可重新加入；`kick` 同时禁领，直到 `unblock`；已禁领的用户须先解除。owner自身不可踢。禁领不随历史清理失效。
- POST `/sources/{id}/link/rotate` → `{url:"/link/{token}"}`，仅owner／admin，旧直链失效。
- GET `/link/{token}?format=clash|v2ray|auto`（旧站路径 `/l/{token}`、`/give/{token}` 等价）无API前缀和Cookie，下载整个来源，绕过个人席位／等级，不附个人保活；来源和owner必须启用。持有链接即可访问。

## 节点

GET `/nodes?page=1&page_size=50&q=&type=&countries=JP,US&min_level=0..6&source_id=&provider_id=&following=1&status=&sort=latency|name|created` → `{items:Node[],total,page,page_size,stats:{total,healthy,unhealthy,pending}}`。`min_level`精确匹配来源门槛，有多个来源时任一匹配即可。

兼容旧 `country` 和 `max_latency` API 参数；网页已移除最大延迟筛选和相关 URL 状态。

`Node = {id,name,type,server,port,latency_ms:number|null,status:"unknown"|"healthy"|"unhealthy",country,country_code,region,city,exit_ip,last_checked_at,next_check_at,last_error,sources:[{id,name,node_name,owner_id,provider_name}]}`。

普通用户的节点列表按“可选 → 席位已满、流量耗尽（`unavailable=exhausted`）、套餐到期（`expired`）等暂不可选 → 检测不可用”分层，层内再按所选排序；来源推荐排序同理（没有可用节点的来源最后）。

Node另带`insights:{v2ray_compatible,v2ray_reason,cipher?,transport?,tls,warnings:[{code,severity,message}]}`，Origin另带`min_level,subscription_usage?`；只分析配置，不保证实际客户端连通，不输出凭据。TLS状态按协议判断，不把SS AEAD／SS2022／WireGuard泛化为明文。

`SubscriptionUsage={upload_bytes,download_bytes,total_bytes,remaining_bytes,expires_at,status,fetched_at}`，缺失值为null，未提供头则整个对象省略；来源级元数据，不是个人用量。`status=unknown|normal|low|exhausted|expired|invalid`，low为已知剩余≤10%；total=0表示未知，整数／重复／溢出严格校验。到期时间为UTC ISO，读取时刷新到期状态。

- POST `/nodes/{id}/test` → `{queued:true}`，仅管理员。
- POST `/nodes/test` `{ids?:number[]}` → `{queued:true}`，仅管理员；省略 ids 检测全部有效节点。
- GET `/options` → `{types:string[],countries:[{code,name}]}`。

检测失败按 1／2／4／8／16／24 小时退避，之后最多 24 小时；连续 7 天无成功检测则屏蔽。屏蔽节点不计入正常列表、统计、选项或无 ID 的批量检测。明确 ID 的手动复测仍可恢复屏蔽节点，成功后清零失败，兜底 9 小时后再测；订阅来源刷新后会更早重排。`blocked`、`last_success_at` 为内部字段。

## 组合订阅

`Subscription = {id,name,node_count,source_count,created_at,updated_at,url,nodes:[{node_id,source_id}],node_ids:number[],source_ids:number[],follow_source_ids:number[],follow_details?:FollowSource[],follow_node_count:number,rule_profile_id:number|null}`。`url` 是相对地址 `/sub/{token}`，前端复制时补当前站点源；新令牌为 32 位小写十六进制，旧的 64 位令牌继续有效。

`FollowSource = {source_id,name,node_count,available,min_level,max_users,subscription_usage?,provider_id,provider_name,provider_avatar,provider_title}`，`node_count` 是来源当前可用节点数，`follow_node_count` 是可用跟随来源的节点数之和（跨来源可能有重复）。

- GET `/subscriptions` → `{items:Subscription[]}`，仅本人。

GET列表另带`node_details:[{node_id,source_id,name,source_name,type,server,port,available,insights,min_level,subscription_usage?}]`，不含凭据。保存只提交ID对，关系删除后保留ID并标为不可用。

- POST `/subscriptions` `{name,nodes:[{node_id,source_id}],source_ids:number[],follow_source_ids?:number[],rule_profile_id?:number|null}` → `{subscription,truncated,selected_count}`，201。`selected_count` 含跟随来源当前的可用节点数。
- PUT `/subscriptions/{id}` 同输入／输出，200，仅本人。
- PATCH `/subscriptions/{id}` `{rule_profile_id:number|null}` → Subscription，200；仅更新规则绑定，不重新校验或领取历史快照节点。即使来源已停用／删除也可切换或解绑，保留节点清单及令牌。
- DELETE `/subscriptions/{id}` → `{deleted:true}`，原链接立即失效。
- POST `/subscriptions/{id}/rotate` → Subscription，旧令牌立即失效。

显式节点须附真实来源归属；保存按账号全部组合合计去重计额，事务锁定账号及来源以防并发超领。非空 `source_ids` 表示主动选择整个来源，按来源和节点 ID 升序填入剩余额度，超出部分截断并返回提示。输出 `source_ids` 是实际选中节点的来源集合。**原样编辑快照时提交已有 `nodes`，并令 `source_ids:[]`**；把响应 source_ids 直接回传会重新进行整来源选择。

`follow_source_ids` 表示跟随整个来源（网页的整来源选择即使用此字段）：下载时实时输出来源全部可用节点，来源增删节点自动同步，**不计节点额度，但每个来源计入来源额度**（`max_users=0` 的不限人数来源不计），同时领取该来源席位。同一来源的单选节点和 `source_ids` 会被忽略。跟随与单选一样受风控：北京时间每日新增跟随来源数默认等于来源额度（`risk_daily_new_sources`，0 表示跟随来源额度），窗口内跟随过的不同来源不超过 `来源额度 × risk_exposure_multiplier`，超出返回 429；已跟随或窗口内跟随过的来源不算新增。被踢出、来源被删除或下架时跟随会被移除并存入历史组合。

保存后固定节点快照。输出过滤已删除关系、排除／屏蔽节点、停用来源及停用提供方，不补入用户未选择的新节点；同时按最新账号额度重新裁切。降低订阅条数额度时，按已有订阅顺序限制可用输出。

PUT 覆盖会先排除目标组合的旧用量再校验账号合计额度，保留原令牌。网页保存弹窗可选择新建或覆盖，订阅数满额时仍可选节点并覆盖。`rule_profile_id=0` 绑定官方默认规则，正整数仅可绑定本人私人规则，显式 `null` 不绑定。新建省略默认 0；编辑省略保留原值，旧 null 不强制迁移；负数或错误类型返回 400。PATCH 必须显式提供该字段。

- GET `/subscriptions/history?page=&page_size=` → `{items:[{source_id,source_name,action,created_at}],total,page,page_size,history_retention_days}`，本人来源领取／释放等事件。
- GET `/subscriptions/archives?page=&page_size=` → 同分页结构，items为`{id,subscription_id,name,nodes,source_ids,follow_source_ids,follow_details,node_details,rule_profile_id,reason,archived_at}`。reason为`replaced|deleted|source_removed`；显示快照不含token／url／连接凭据，不可恢复旧下载资格。

- POST `/subscriptions/archives/{id}/restore` `{subscription_id?}` → `SavedSubscription & {skipped}`。省略或为 0 时新建订阅（受订阅条数额度限制），否则覆盖本人该订阅并保留链接；被覆盖的组合同样存入历史。快照中已不可用（来源删除／停用／节点下线）的节点和跟随来源跳过并计入 `skipped`，全部不可用返回 409。

历史默认30天，查询按保留窗口过滤并每分钟清理，禁领独立保留。历史组合（archives）每人只保留最近 10 份，写入时清理更早的。网页把 `/subscriptions/history` 称为“席位记录”：记录各来源席位的占用、续期、释放与到期，用于排查节点为何不见。过期个人账号不能新增／覆盖组合，应先网页激活。

## 规则组合

`RuleProfile = {id,name,official:boolean,readonly:boolean,routing_enabled,dns_enabled,dns_servers:string[],rules:string[],created_at,updated_at}`。官方内置项 ID 为 0，`official/readonly` 均 true，所有账号直接可用，无需创建数据库副本；正整数为账号私有组合，两标记为 false。

- GET `/rule-profiles` → `{items:RuleProfile[],limit}`，官方项始终排第一，其后仅本人私人组合；`limit=10`，每人最多 10 份私人组合，超出 POST 返回 400。
- GET `/rule-profiles/default` → 完整官方模板，与列表中 ID 0 内容一致。
- POST `/rule-profiles` 同上配置字段 → RuleProfile，201。
- PUT `/rule-profiles/{id}` 同上配置字段 → RuleProfile。
- DELETE `/rule-profiles/{id}` → `{deleted:true}`；仍被订阅绑定时 409，须先解绑。

PUT／DELETE `/rule-profiles/0` 返回 403，官方规则不可修改或删除。复制通过 POST 提交配置字段 `name,routing_enabled,dns_enabled,dns_servers,rules` 创建私人项；响应元数据不属于写入字段。

配置字段另含`groups:RuleGroup[]`，`RuleGroup={name,type,include_all,node_ids,countries,groups,country_groups,interval,tolerance}`。`node_ids` 已停用：写入时清空、导出忽略，策略组只按“全部节点／国家／其他策略组”组合，避免借规则夹带额外节点。`country_groups` 默认 true：`select` 且包含全部节点的组会按本次订阅出现的国家自动追加 `🇭🇰 香港` 这类 url-test 分组（每国一个，组名不与已有组冲突），国家分组定义放在配置末尾。成员顺序：引用的策略组 → 国家分组 → 节点。type为select／url-test／fallback／load-balance；最多64组，必须含NodePool，名称唯一≤100字符、不含控制字符／逗号、不能用DIRECT／REJECT／GLOBAL；每组最多2000正节点ID／64国家／64引用。interval默认300、60–86400秒；tolerance默认50、0–1000毫秒。旧NULL／省略维持默认七组。引用DIRECT／REJECT或其他组，禁止悬空、自环和循环。国家只在本次成功导出的已选节点中匹配，不额外领取。拦截广告这类组是成员 `[REJECT, DIRECT]` 的手动选择组，首项 REJECT 即默认拦截，客户端可切到 DIRECT。HK／TW／MO独立，CF按现有节点分类视为Cloudflare而非国家；空组DIRECT回退。

默认分流沿用本机“订阅共享”的完整规则表，主代理组名统一为 `NodePool`，包含自动选择、故障转移、Cloudflare、国内网站、拦截广告和漏网之鱼。规则按序匹配，可编辑；不加入品牌或流量信息假节点。默认 DNS 开启且留空时写入 `223.5.5.5`、`119.29.29.29`、`8.8.8.8`。关闭 DNS 则不写入 DNS 段。

仅 Clash 应用规则组合，v2ray 导出保持节点 URI。未绑定规则组合的旧订阅维持现有仅节点配置。关闭分流则从已缓存来源配置合并可保留的本地规则／策略组，按来源稳定排序并区分同名引用，只包含本次选中节点；无来源规则时使用 `MATCH,NodePool` 兜底。存储与校验中主策略组仍为 `NodePool`；Clash 导出时主组及所有引用、规则目标统一显示为 `MCII节点共享`，该名称不可用作自定义组名，同名节点自动加序号。

自定义规则支持 `DOMAIN`、`DOMAIN-SUFFIX`、`DOMAIN-KEYWORD`、`IP-CIDR`、`IP-CIDR6`、`DST-PORT`、`SRC-PORT`、`NETWORK`、`GEOIP(CN/LAN)`、`MATCH`，IP／GEOIP 支持 `no-resolve`；目标限定为上述策略组、DIRECT 和 REJECT。最多 20,000 条、每条 4,096 字节、合计 2 MiB。DNS 最多 32 项，支持 IP 或 TLS／HTTPS／UDP／TCP 地址。

跟随来源时保留 `select/url-test/fallback/load-balance` 的本地已选成员，采用安全默认组参数及固定 gstatic 探测地址。外部 `proxy-providers`、`rule-providers`、`use`、`RULE-SET` 不透传；不支持的规则、空组、无效引用计入 `X-Subscription-Routing-Skipped` 响应头。多来源具体规则按来源 ID／内部顺序匹配，末尾使用最小来源 ID 的首个有效 MATCH。节点 `dialer-proxy` 依赖须在本来源已选节点中，缺失或循环返回 422，避免输出失效配置。

## 客户端下载

GET `/sub/{token}?format=clash|v2ray|auto`，**无 `/api/v1` 前缀**，无 Cookie。

- `clash`：包含完整代理配置、选择组和规则的 Clash／Mihomo YAML。
- `v2ray`：UTF-8 分享 URI 按行连接后 Base64。支持可以完整表达的 SS／SS2022、VMess、VLESS、Trojan、Hysteria2、TUIC、HTTP／SOCKS；不能保留参数的节点会跳过，全部无法表达则 422。
- 证书指纹：Xray 新版移除了 `allowInsecure`，只有它而没有 `pinnedPeerCertSha256` 时内核拒绝启动，新版 v2rayN 也不再写入它。服务端每 5 分钟为跳过证书验证（`skip-cert-verify`）的 VMess／VLESS／Trojan TLS 节点（非 Reality）和无混淆的 Hysteria2 节点直连读取证书（只连公网地址），记下叶子证书与签发证书的 SHA256（`np_node_tls`，成功后每天复查，失败 6 小时后重试并保留旧值）。v2ray 导出保留 `allowInsecure=1`／`insecure=1` 兼容旧内核，另附 `pcs=<叶子>,<签发>`（VMess JSON 为 `pcs` 字段），Hysteria2 写 `pinSHA256=<叶子>`；节点自带 `fingerprint` 时优先使用。导入分享链接时 `pcs` 保留为节点的 `fingerprint`。Clash 导出不变。
- `auto` 或省略：按 User-Agent 选择。Mihomo／Clash／Stash 返回 Clash，v2rayN／v2rayNG 等兼容客户端返回 URI 订阅，未知 UA 默认 Clash。显式格式优先。

URI 订阅只承诺兼容 URI 导入；不提供 sing-box JSON，不把 URI 集合伪装为 JSON。v2ray 输出 v2rayN 等客户端通用的分享 URI：只要求连接必需的信息能完整表示，TFO、多路复用、UDP 开关、early data、除 Host 外的伪装请求头等客户端调优参数不保留；链式代理、未知协议、未知传输方式和插件（如 shadow-tls）无法连接，跳过并通过 `X-Subscription-Skipped` 报告数量。全部无法输出时返回只含一条提示节点的订阅（HTTP 200），避免客户端只报“无效的订阅内容”。支持 ss（obfs、v2ray-plugin）、vmess、vless（含 reality）、trojan、hysteria2（`mport` 端口跳跃）、tuic、anytls、http、socks5；传输方式 tcp、ws、httpupgrade、grpc、h2、http、xhttp。

响应提供 `X-Subscription-Format`、`X-Subscription-Skipped`、`X-Subscription-Routing-Skipped`；v2ray 另带 `X-Subscription-Compatibility: v2ray-compatible URI import only`。设置合理 Content-Type、inline 文件名、`Cache-Control: no-store, private, max-age=0`、`Vary: User-Agent` 与 `Referrer-Policy: no-referrer`。

`/sub/` 与 `/link/` 下载均带 `Content-Disposition: attachment; filename="MCII"; filename*=UTF-8''MCII%E8%AE%A2%E9%98%85%E5%85%B1%E4%BA%AB` 及 `Profile-Title: base64:…`，客户端显示订阅名「MCII节点共享」，不再显示令牌。个人下载另带`X-Subscription-State:active|expired`、`Profile-Web-Page-Url`和`Subscription-Userinfo: expire=个人租期Unix秒`。到期返回200提示节点，不输出真实节点。有效下载附保活VLESS/WS节点及独立hidden url-test组；只有固定HTTP gstatic204的HEAD／GET有效探测续期，旧过期UUID不能复活。GET `/ws/lease/{uuid}`仅WebSocket协议入口，不提供通用转发。公网源优先settings.public_url，否则按请求TLS／Host推断，不信任转发头；反代须配置真实HTTPS源和WS Upgrade。来源`/link/`转发已解析来源用量头，不套个人到期。

实测回报：开启 `beacon_enabled` 的用户下载 Clash 格式时，订阅另附最多 15 个隐藏 url-test 组（`实测回报 N`），每组只含一个节点，检测地址为 `http://<公网主机>/b/<32位令牌>`，间隔 1 小时、`lazy:false`。令牌按（批次，节点）随机生成，同一订阅 1 小时内复用同一批次，之后轮换：优先选统计窗口内该用户测得次数少（未测的最先）、其次全站实测人数少的节点。GET／HEAD `/b/{token}` 始终返回空 204；首次回报记录该节点连通。批次已有回报 10 分钟后仍未回报的节点视为该用户连不上：订阅中排到末尾，订阅详情 `node_details[].my_failed=true`。拉取订阅的 IP 属于中国大陆（内嵌 APNIC 数据，`scripts/update-cnip.py` 更新）时批次计入国内统计；节点列表 Node 另带 `field_test:{ok,fail,days,paused}`：统计窗口（`settings.field_test_days`，默认 3 天）内按用户去重的国内实测结果；`paused=true` 表示本机状态健康、窗口内多数国内用户能连、最近一次国内回报为能连且在 24 小时内，本机暂停检测，延迟为最后一次本机结果；有人连不上或超过 24 小时无人回报后恢复。连不上不会有任何请求到达服务器：同一批次已有其他节点回报（证明客户端在线并已加载订阅）10 分钟后仍未回报的节点记为连不上。回报走 HTTP 80 端口以免每次握手，反代须让 `/b/` 不跳转 HTTPS。批次保留 8 天。

国内实测参与排序：每个节点按窗口内结果分档——能连用户占比 ≥70% 为“能连”，无数据为“未知”，至少 2 位连不上且能连占比 <30% 为“连不上”，其余为“不一”。`sort=latency` 与 `sort=recommend` 在可选性分组内先按该档位、再按本机延迟；本机不可用但国内能连的节点不再排到末尾，国内连不上的节点排到末尾。

## 积分、头像与反馈

- GET `/me` 额外返回 `limits:{nodes,sources,subscriptions,level,personal,boost}`；GET `/users` 额外返回 `limits` 与 `risk` 两个以用户 ID 为键的映射。
- GET `/me/wallet` → `{user,limits,boosts,titles,makeup_used,leave_used,missed_days,today_share_xp,rules,achievements}`。
- POST `/me/makeup`、POST `/me/leave`、POST `/me/boosts {kind:node|source|subscription}`、POST `/me/titles {name}`、PUT `/me/title {name}`（空字符串取下）、POST `/providers/{id}/tip {amount}` 均返回新的 wallet；积分不足 409，超次数 409。
- GET `/me/ledger?page&page_size` → `{items:[{id,kind,xp,points,note,created_at}],total,page,page_size}`。
- POST `/me/avatar`（PNG／JPEG／WebP 原始字节，≤200KB）→ user；DELETE `/me/avatar` → user；GET `/avatars/{id}?v=` 返回图片，需登录。
- GET `/feedback?target=node|source|all&node_id&source_id&page&page_size` → `{items,total,page,page_size,visible_days}`；POST `/feedback {target,node_id?,source_id,kind,comment}`，同一对象 24 小时内覆盖；PATCH `/feedback/{id} {resolved}`（提供方／管理员）与 DELETE `/feedback/{id}`（本人／管理员）返回 `{ok:true}`。
- POST `/users/{id}/risk {freeze_hours?,reset_exposure?}`（管理员）→ 风控摘要；`freeze_hours:0` 解除冻结。
- 风控拒绝保存订阅返回 429；`/sub/` 拉取超限返回 429 与 `Retry-After: 600`；数据密钥错误时服务返回 503。

## 设置和版本

- GET `/settings` → `{exclude_pattern,test_url,timeout_ms,probe_concurrency,source_concurrency,refresh_hours:8,check_hours:8,subscription_lease_days:14,history_retention_days:30,field_test_days:3,idle_check_days:3,public_url:""}`。
- GET／PUT `/settings` 另含 `community`（等级额度、经验积分、兑换价格、称号、风控与反馈参数），旧前端不提交时保留已保存值；服务端统一校验范围。
- PUT `/settings`提交以上可编辑字段，固定周期不需提交。租期1–365天、历史1–3650天、实测窗口1–7天、闲置检测间隔0–30天（0为不定期检测，旧前端不提交时保留原值），旧缺字段使用默认值；public_url可空，否则必须无用户信息／路径／查询的HTTP(S)源地址。排除正则变化按缓存重算，失败保留旧列表。
- GET `/releases` → `{current_version,latest_version,update_available,can_update,can_rollback,need_restart,state,message,releases:[{version,name,body,published_at,url}]}`。
- POST `/system/update` `{version?:string}` → `{queued:true}`，默认最新稳定版。
- POST `/system/rollback` `{version?:string}` → `{queued:true}`，空值恢复本地 backup，否则安装指定历史版。
- POST `/system/restart` → `{restarting:true}`，Linux 退出后由 Docker restart 策略拉起。

这些接口仅管理员可用。更新／回退异步执行，通过 `/releases` 轮询。回退只处理程序文件，不处理数据库。

## 网页路由与外观

- `/nodes`：节点页，支持分页、协议、可搜索分页来源、国旗网格地区多选、状态、排序、提供方及关注范围。
- `/sources`：共享来源（本人来源，管理员可管理全部），页首含可折叠的共享玩法说明；`q` 保存来源名称搜索。
- `/providers`：提供方目录，`q`、`following`、`page` 可还原。
- `/subscriptions`、`/account`：个人组合订阅和账号额度／设置。
- `/rules`：查看官方默认规则，复制为私人组合；私人工作台支持搜索、每页30条、逐条增改、上下移动、文本编辑、DNS分区和撤销本次修改。
- `/users`、`/statistics`、`/settings`：管理员页面；`/statistics` 每 4 秒采样，离开页面停止请求。

分页控件显示首页、尾页和当前页前后 2 页，其余以不可点击的省略号表示，例如 `1 2 3 … 59` 与 `1 … 6 7 8 9 10 … 59`。

默认卡片；`page=1`、`page_size=50`、`sort=latency`、`view=cards` 从 URL 省略。离散筛选、分页和视图切换保留历史，搜索输入替换历史。节点选区在当前登录会话内跨页保留，退出或账号失效时清空。

节点默认整卡点击查看详情，进入选择模式后整卡选择，支持键盘操作。卡片单行标题省略并提供悬浮全文。桌面地区面板每行 5 项，名称一行、内部滚动；整来源弹窗并列显示当前选区与来源的选择状态。

地区选中项带勾选标记，面板显示已选数量和可取消标签，触发器显示已选名称；“完成”关闭后恢复焦点。当前选区按真实来源名称分组，统一“移除”控件。弹窗遮罩关闭要求按下与松开都在外部、移动不超过6px；内容拖出、外部拖入、内部留白均不误关，Escape与关闭按钮仍可用。

首次跟随系统亮暗，太阳／月亮图标切换后保存显式偏好。版本入口显示后端真实版本文字；普通用户仅查看版本，管理员打开更新弹窗。顶栏不显示正常数据库连接说明，异常仍有明确提示。

## 维护模式、趋势与称号

- 访客浏览：`community.guest_browse` 默认 true，未登录也可 GET `/nodes`、`/options`、`/sources/catalog`、`/providers`、`/levels`、`/titles`、`/rule-profiles/default`，以及只含 `{name,version,maintenance}` 的 `/system`；其余接口与所有写操作仍返回 401。访客看不到节点服务器地址（与普通用户相同按提供方遮蔽）。
- 风控：`community.risk_pulls_per_hour` 默认 120（升级时把旧默认 60 一次性改为 120，手动改过的值保留）。
- 节点库：`np_nodes` 是去重后的节点库，包含已排除的信息节点和上游已移除的节点；只有启用来源中仍关联且未排除的节点参与检测和展示。无任何来源关联的节点标记 `orphaned_at`，每小时清理超过 7 天的孤立节点。
- 个人订阅下载不再被覆盖成 `filename="subscription.yaml"`，统一使用上文“客户端下载”中的 MCII 文件名头。
- 编辑来源：只有 URL 实际变化时才重新拉取；排除规则变化或重新启用时只用缓存内容重新解析；仅改名称／人数等不触发刷新。
- `Settings.maintenance = {enabled,message(≤300字),since}`，通过 PUT `/settings` 修改；GET `/system` 对所有登录用户返回 `maintenance`。开启后非管理员的非 GET 请求返回 503（`/auth*` 与 `/me/checkin` 放行），订阅 `/sub/` 拉取不受影响。
- GET `/titles` → `{items:[{name,color}]}`；`color` 取 `amber|blue|green|violet|red|pink|cyan|slate|gold`，在 `community.titles[].color` 设置。
- GET `/statistics` 增加 `traffic:{hourly:[{at,bytes}]×24,daily:[{at,bytes}]×30}`，由每小时来源快照中已用流量（upload+download）的差值得出，减少视为套餐重置并取当前值；缓存 5 分钟。
- GET `/me/provider-trend?days=7..30` → `{days:[{day,users,nodes,healthy,bytes}]}`，汇总调用者拥有的全部来源；人数不含提供方本人。
- 每小时任务：`community.usage_poll_enabled=true` 时对 30 分钟内未刷新的启用 URL 来源发送一次 GET，只读取 `Subscription-Userinfo` 响应头（不读正文），再写入 `np_source_hourly`（保留 36 天），并清理过期会话。
- 反馈：提供方把问题反馈标为已处理时，反馈人获得 `feedback_reward_xp`／`feedback_reward_points`（每条一次，每日最多 `feedback_reward_daily` 次，流水类型 `feedback`）。24 小时内 ≥ `feedback_demote_users` 名不同用户反馈不可用／慢／不稳定且未处理的节点，在订阅导出中排到末尾，`node_details[].demoted=true`；`feedback_visible_days` 默认 30。
- `node_details[]` 增加 `provider_id,provider_name,provider_avatar,provider_title`。

## 邮箱、第三方登录与节点可用性

- 运行设置 `login`：`{require_email,email_suffixes[],smtp:{host,port,security:"ssl"|"starttls"|"none",username,password,from,from_name},github:{enabled,client_id,client_secret},linuxdo:{...}}`。GET 不返回任何密钥，只返回 `password_set`／`secret_set`；PUT 留空沿用旧值，新值启用数据加密时以 `np_settings.secret` 用途加密保存。未提交 `login` 时保留原值。默认允许后缀 21 个（deepseek.com、edu.mo、edu.hk、edu.tw、foxmail.com、163.com、126.com、139.com、189.cn、edu.cn、gmail.com、aliyun.com、apache.org、sohu.com、privaterelay.appleid.com、xiaomi.com、yahoo.com、sina.cn、sina.com、qq.com、gov.cn），子域名自动匹配，清空表示不限。
- POST `/settings/smtp-test` `{to}` → `{ok}`，仅管理员，使用已保存的 SMTP 配置。
- 头像接受 PNG／JPEG／GIF，GIF 与 APNG 只保留首帧并重新编码为静态 PNG（超过 200KB 时转 JPEG）；前端上传前也会裁剪并转成静态 JPEG。
- GET `/nodes` 新增 `available=1`（只看当前用户可选）和 `sort=recommend`（默认：可选优先，72 小时内新入池的健康节点优先，再按延迟）。所有角色（含管理员）的所有排序都按“可选 → 席位已满／等级不足／已被移出 → 检测不可用”分组，管理员领取同样受席位上限限制，只跳过等级限制；`available=1` 对管理员同样生效。来源目录 `/catalog` 的所有排序同理；节点与 `sources[]` 返回 `unavailable:"full"|"level"|"kicked"`（席位已满／等级不足／已被移出），节点另有 `fresh`。
- GET `/sources/catalog` 新增 `sort=recommend|new|latency|idle`（latency：健康节点平均延迟升序；idle：席位占用比例低的优先，不限人数按使用人数）、`available=1`、`countries=HK,JP`（含任一地区未排除节点的来源，最多 20 个），条目附带 `healthy_count`、`avg_latency`、`countries`（最多 8 个）、`unavailable`、`seated`、`fresh`（7 天内创建）。
- 来源 `max_users=0`（不限人数）时不占用户的来源额度：保存组合、客户端输出与 `/me` 的 `usage.sources` 都只统计设置了人数上限的来源。
- 同一节点由多个来源提供时，保存组合按以下顺序决定实际占用的来源（返回的 `nodes[].source_id` 即实际来源）：不限人数的来源 → 你已占有席位或本次已选用的有限来源 → 仍有空位的有限来源。来源被下架、停用、删除、提高等级要求或提供方被停用后，受影响用户的订阅立即切换到其他可用来源（同样优先不限人数）；拉取订阅时发现失效节点也会自动尝试切换（每用户 5 分钟一次，来源恢复或改为不限人数时重置）。没有其他可用来源时节点保留，来源恢复后继续输出。跟随整个来源不参与切换。
- 来源直链 `/link/{token}` 不计入个人订阅的拉取风控（每小时次数、每日 IP、节点曝光窗口），也不占用席位。
- `/me/provider-trend` 每天带 `sampled`，来源上架前没有快照的日子为 false，前端计算平均健康度时跳过。

## 来源下架、用户详情与公开统计

- POST `/sources/{id}/takedown` `{reason,detail?,notify?}` → `{source,notified}`。`reason` 取 `owner_request`（订阅链接所有方要求下架）、`invalid`、`abuse`、`duplicate`、`risk`、`other`；选 `other` 时 `detail` 必填，最多 500 字。下架后来源 `enabled=false` 且带 `takedown:{reason,reason_label,detail,at,by}`，节点离开节点总览与全部订阅；提供方和管理员都不能通过 PATCH 重新启用（400），只能 POST `/sources/{id}/restore` `{notify?}` 恢复，恢复后重新排队刷新。管理员可处理全部来源；用户管理员只能处理普通用户和自己的来源（403）。`notify` 默认 true：提供方有已验证邮箱且 SMTP 可用时异步发送 HTML 通知邮件。
- GET `/sources/{id}/moderation` → `{items:[{id,action,reason,reason_label,detail,operator,created_at}],takedown}`，提供方本人或有权处理的管理员可见。
- GET `/moderation/sources?owner_id&status=active|takedown|disabled&q&page&page_size` → `{items:Source[],total,takedowns,page,page_size,reasons}`，仅管理员和用户管理员；已下架的排在前面。
- GET `/users/{id}/detail` → `{user,identities,stats,sources}`；`stats` 含共享来源数（上架／已下架）、提供节点、订阅用户、关注者／关注数、组合订阅、占用他人席位、7 天拉取次数与 IP 数、最近拉取、有效会话、最近登录、反馈数、30 天积分获得／消耗与经验。DELETE `/users/{id}/identities/{provider}` 解绑第三方账号。用户管理员只能查看和处理普通用户。
- PATCH `/users/{id}` 新增 `email`：管理员设置的邮箱直接视为已验证，不受后缀限制；空字符串解除绑定；重复 409。GET `/users?q=` 同时匹配邮箱。
- GET `/statistics` 对普通用户和访客开放（访客需开启访客浏览）：去掉任务错误详情、停用来源的流量条目和进程内存／协程数，服务端共享 5 秒缓存；管理员仍拿到完整数据。
- 所有邮件均为 multipart/alternative（纯文本＋HTML），包括验证码、SMTP 测试和下架／恢复通知。

## 链式代理

来源中带 `dialer-proxy` 的节点在解析时把前置节点的完整配置内嵌进节点（内部键 `x-np-dialer`），因此节点可以单独检测、去重和导出。前置可以是同一订阅里的节点，也可以是代理组（取第一个成员）；`DIRECT` 视为无前置，`REJECT`、找不到、循环引用或超过 3 层的节点会被跳过并记入警告。Clash 导出把前置展开为名为 `前置 · 原名` 的隐藏节点，不进入任何策略组，相同前置只输出一次；v2ray URI 无法表达链式代理，这类节点会被跳过。

## 每账号来源上限

非管理员每个账号最多提供 100 个来源（含停用和下架的），超出时 POST `/sources` 返回 400。与订阅侧的“来源额度”（组合订阅可占用的来源数）无关。

## 私有来源与专属

- Source 增加 `private`；本人或管理员读取时附带 `share_count`，目录中被分享的来源带 `shared_with_me:true`。POST／PATCH `/sources` 可传 `private`。
- 私有来源只对提供者本人和分享名单内的用户可见（节点、目录、选用、导出均按此过滤，管理员浏览也不例外，只在 `GET /sources?scope=all` 里可管理）；不受 `min_level` 限制，不占使用者来源额度，不进入提供方列表统计，不计每日分享奖励。
- 公开来源改为私有时，不在名单里的使用者立即释放席位、停止输出；取消分享同样立即释放。
- GET `/sources/{id}/shares` → `{items:[{user_id,username,display_name,avatar_version,enabled,seated,created_at}],limit:200}`，仅 owner／admin。
- POST `/sources/{id}/shares` `{user:"12"|"#12"|"username"}` → 201 同形条目；不能分享给本人、已停用账号或重复添加，每个来源最多 200 人。
- DELETE `/sources/{id}/shares/{userID}` → 204。
- GET `/nodes` 与 `/sources/catalog` 支持 `exclusive=1`：只看本人的来源和分享给自己的私有来源（“专属”标签）。

## 本站流量

- GET `/statistics` 对管理员增加 `site_traffic:{hourly:[{at,kind,detail?,requests,failures,bytes_in,bytes_out}],daily:[{at,kind,requests,failures,bytes_in,bytes_out}]}`：hourly 为近 24 小时按分类和明细（延迟检测、出口查询按节点协议，套餐用量读取分 HEAD／GET），daily 为近 30 天按分类。普通用户和访客不返回该字段。
- 分类：后台任务（延迟检测、出口国家查询、证书指纹读取、来源刷新、套餐用量读取）、对外服务（Clash／v2ray 订阅拉取、来源直链、实测回报、席位保活、网页接口、静态资源），以及不计入占比的数据库和健康检查；另附容器网卡计数用于对照。
- 计数为应用层字节，不含 TCP/IP 包头、重传和 DNS；订阅和网页为压缩前大小。进程内每分钟写入 `np_site_traffic`，保留 35 天。
