# 多用户、提供方与订阅实现约定

状态：前后端实现完成，隔离数据库和本地浏览器集成验证通过，详见 verification.md。按登录账号领取提供方来源，客户端用个人令牌链接刷新，不依赖登录 Cookie；公开自助注册仍延期。

## 产品规则

- 现有管理员 ID=1 和现有来源保留。管理员创建/停用用户、重置密码、修改额度。普通用户无需审核即可上传并管理自己的来源；只管理员可改全局设置、运行更新/回退/重启、批量探测。
- 普通用户默认总额度：200 个唯一节点、3 个不同来源、1 条组合订阅。管理员可逐用户调整，额度是账号全部订阅合计；保存与输出均按最新额度校验/裁切。0 不代表无限额度，管理员可配置正整数；来源 max_users=0 表示不限领取用户。
- 每个上传来源可设置 max_users；领取后 np_source_claims 按 (source_id,user_id) 唯一，重复选取/刷新/多个节点都只计一次，保存事务成功才占名额，失败不占。领取为历史唯一账号计数，取消关注/删除组合不抹除领取记录。已有领取人在来源人数上限降低后可继续获取，超额仅禁止新领取。
- 全部共享节点、关注提供方的节点、指定提供方节点可查看。关注与领取独立，关注不占来源名额。
- 来源全选按节点 ID 升序填入剩余额度，去重后截取前 N（默认200），显示截断结果；显式节点必须带来源归属，来源不能冒用，重复节点只算一次。组合订阅保存选中节点快照；来源刷新后失效/排除/屏蔽节点在输出时过滤，不替换为用户未选择节点。
- 提供方专属分享入口 /nodes?provider_id={userID}；登录后查看/领取该提供方来源，可复制入口。订阅客户端下载使用 /sub/{token}，token 对应已登录账号创建的组合，支持重置及撤销。
- 更新和数据库状态不在普通顶栏展示；异常保留明确错误提示。

## JSON API（同一 /api/v1 前缀）

User = {id,username,display_name,role:"admin"|"user",enabled,node_limit,source_limit,subscription_limit,created_at}
- GET /auth -> {authenticated,username,user:User|null}；POST /auth/login {username?,password} -> 同形。username省略兼容admin。POST /auth/logout 保持兼容。
- GET /me -> {user:User,usage:{nodes,sources,subscriptions},provider_path:string}
- PATCH /me {display_name?,password?} -> User；修改密码撤销其他会话。
- GET /users?q=&page=1&page_size=20 -> {items:User[],total,page,page_size}（admin）
- POST /users {username,password,display_name,node_limit,source_limit,subscription_limit} -> User 201
- PATCH /users/{id} {display_name?,password?,enabled?,node_limit?,source_limit?,subscription_limit?} -> User

Source 在已有响应上增加 {owner_id,provider_name,max_users,claimed_users,active_users}；新建/编辑支持 max_users。claimed_users 保留历史唯一领取，active_users 按仍有组合引用来源的启用账号去重，删除最后引用或覆盖来源会下降。GET /sources 为本人管理来源（admin全部）。写/删/刷新检查所有权或admin；原始URL和节点凭据永不展示。
- GET /sources/catalog?q=&provider_id=&following=1&page=1&page_size=20 -> {items:Source[],total,page,page_size}（只共享可用来源）；供可搜索分页来源选择与整来源选取。
- GET /providers?q=&following=1&page=1&page_size=20 -> {items:Provider[],total,page,page_size}
  Provider = {id,username,display_name,source_count,node_count,followers,followed,provider_path}
- POST /providers/{id}/follow；DELETE /providers/{id}/follow -> {followed:boolean}

Node.sources 的 Origin 增加 {owner_id,provider_name}。Nodes查询新增 provider_id、following=1、countries=JP,US（兼容旧country）。Options仍types/countries；可附count但前端不依赖。
- GET /nodes 保持原分页协议，浏览正常有效共享节点；所有手动节点检测仅admin。所有普通API都验证账号enabled。

Subscription = {id,name,node_count,source_count,created_at,updated_at,url:string,nodes:[{node_id,source_id}],node_ids:number[],source_ids:number[],rule_profile_id:number|null}
- GET /subscriptions -> {items:Subscription[]}
- POST /subscriptions {name,nodes:[{node_id,source_id}],source_ids:number[]} -> {subscription:Subscription,truncated:boolean,selected_count:number} 201
- PUT /subscriptions/{id} 同输入 -> 同输出 200
- 编辑时提交原有 nodes，并令 source_ids=[] 可保留快照；响应 source_ids 为实际选择的来源集合，不应直接回传作为整来源扩选。
- DELETE /subscriptions/{id} -> {deleted:true}
- POST /subscriptions/{id}/rotate -> Subscription；旧token立即失效。
- GET /sub/{token}?format=clash|v2ray|auto（不带/api/v1）客户端公开令牌入口。显式格式优先；auto按UA决定，默认clash。clash返回有效可导入完整YAML配置，v2ray返回UTF8分享URI按行连接再base64；支持SS、VMess、VLESS、Trojan、Hysteria2、TUIC、http/socks有条件转换；不支持协议/参数不能生成丢配置假节点，应跳过并返回跳过数Header，全部不可转换则422。停用用户或来源/账号无权限立即禁止输出；Content-Type/Cache-Control/Vary/User-Agent及inline文件名合理。令牌不计入请求日志。

规则组合 API：GET /rule-profiles -> {items:RuleProfile[]}，首项为所有账号可用的官方内置规则（id=0、official=true、readonly=true），其后为账号私人规则（正数ID、两个标记false）。GET /rule-profiles/default 返回完整官方模板，POST 创建私人副本，PUT/DELETE /rule-profiles/{id} 修改私人规则；官方0修改或删除403，被绑定私人规则删除409。RuleProfile = {id,name,official,readonly,routing_enabled,dns_enabled,dns_servers:string[],rules:string[],created_at,updated_at}。订阅 rule_profile_id=0 绑定官方，正数仅本人私人项，null不绑定；新建省略默认0，编辑省略保留，旧null不迁移，负数拒绝。PATCH /subscriptions/{id} 只改规则绑定，不重新校验／领取节点快照。仅Clash应用，DNS留空默认三个地址、关闭不写DNS；分流默认沿用本机订阅共享完整规则，关闭合并缓存源本地分流；v2ray不变。

## 界面

- 亮暗图标一键切换，首次跟系统；版本为纯文字可聚焦触发器，显示实际版本不伪造v0.0.1。
- 去掉顶栏数据库连接展示、侧栏自动维护说明；错误仍展示。
- 节点搜索、协议、可搜索来源、国家/地区多选、状态、排序、重置图标在一条工具栏；桌面一行，窄屏允许横向工具条/合理紧凑换行，不挤坏控件。移除最大延迟UI及新URL状态。
- 来源面板参考 Token分享 account-table.html 的内置搜索、勾选摘要与清空；地区网格参考本机订阅共享 CatalogView.vue／RegionCell.vue。复用自绘可访问浮层，不依赖原生 select。
- 节点可多选，展示提供方、可关注/按提供方查看。提供方页分页搜索和关注；来源弹窗可搜索分页选择整个来源；组合页查看/编辑/复制自动、Clash、v2ray链接及重置令牌、删除。
- 紧凑卡片单行标题并悬浮全文；浏览整卡看详情，选择模式整卡选择。检测仅管理员详情。桌面地区五列、单行名称、内部滚动；整来源弹窗显示当前选区和每来源状态。保存弹窗可覆盖已有组合并保留原链接，满额仍能进入选择。
- 地区选中项显示勾选，已选数量和可取消名称标签置于搜索上方，触发器显示所选名称。当前选区按真实来源名称分组，统一紧凑移除按钮。
- 官方规则可只读预览和复制；私人规则工作台提供搜索、每页30条、逐条增改、上下移动、文本模式、DNS分区、未应用规则提醒与撤销本次修改。
- 遮罩关闭须在弹窗外按下并松开且未拖动；内容拖出、外拖入、内部留白不触发关闭。
- 管理员用户管理显示和编辑额度；普通用户账号页显示额度/已用、自己的提供方链接。
- 所有表单字段统一：第一行标签（可选标记同行），第二行输入，第三行说明（可选）；并排字段顶端/输入框对齐，禁用额外的可选标记独占行。

## 最小必要验证

权限越权/停用会话、来源所有权、管理员接口、并发名额唯一计数/上限、去重和额度/全选裁切、token撤销、格式往返/配置不丢失、原调度回归；隔离MariaDB，无业务库。前端类型/构建及一轮Edge契约回归，更新旧选择器。
