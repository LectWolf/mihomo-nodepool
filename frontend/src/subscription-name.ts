// 订阅在客户端中的显示名称；主策略组内部仍以 NodePool 存储
export const subscriptionDisplayName = 'MCII节点共享'
export const mainGroupLabel = subscriptionDisplayName
export function groupLabel(name: string) { return name === 'NodePool' ? mainGroupLabel : name }
