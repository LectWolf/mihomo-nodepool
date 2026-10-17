package pool

import (
	"fmt"
	"sort"
	"strings"
)

// countryGroup 是导出时按订阅节点所在国家自动生成的分组
type countryGroup struct {
	Code string
	Name string
}

var countryNames = map[string]string{
	"HK": "香港", "TW": "台湾", "MO": "澳门", "CN": "中国", "JP": "日本", "KR": "韩国", "SG": "新加坡", "US": "美国",
	"GB": "英国", "DE": "德国", "FR": "法国", "NL": "荷兰", "CA": "加拿大", "AU": "澳大利亚", "NZ": "新西兰", "RU": "俄罗斯",
	"IN": "印度", "TH": "泰国", "VN": "越南", "MY": "马来西亚", "PH": "菲律宾", "ID": "印度尼西亚", "TR": "土耳其", "AE": "阿联酋",
	"IL": "以色列", "SA": "沙特阿拉伯", "BR": "巴西", "AR": "阿根廷", "CL": "智利", "MX": "墨西哥", "CO": "哥伦比亚", "PE": "秘鲁",
	"ZA": "南非", "EG": "埃及", "NG": "尼日利亚", "KE": "肯尼亚", "IT": "意大利", "ES": "西班牙", "PT": "葡萄牙", "CH": "瑞士",
	"AT": "奥地利", "BE": "比利时", "SE": "瑞典", "NO": "挪威", "FI": "芬兰", "DK": "丹麦", "IE": "爱尔兰", "PL": "波兰",
	"CZ": "捷克", "HU": "匈牙利", "RO": "罗马尼亚", "BG": "保加利亚", "GR": "希腊", "UA": "乌克兰", "LU": "卢森堡", "IS": "冰岛",
	"EE": "爱沙尼亚", "LV": "拉脱维亚", "LT": "立陶宛", "RS": "塞尔维亚", "HR": "克罗地亚", "SK": "斯洛伐克", "SI": "斯洛文尼亚", "MD": "摩尔多瓦",
	"KZ": "哈萨克斯坦", "UZ": "乌兹别克斯坦", "MN": "蒙古", "KH": "柬埔寨", "LA": "老挝", "MM": "缅甸", "BD": "孟加拉国", "PK": "巴基斯坦",
	"NP": "尼泊尔", "LK": "斯里兰卡", "IR": "伊朗", "IQ": "伊拉克", "QA": "卡塔尔", "KW": "科威特", "BH": "巴林", "OM": "阿曼",
	"JO": "约旦", "CY": "塞浦路斯", "MT": "马耳他", "GE": "格鲁吉亚", "AM": "亚美尼亚", "AZ": "阿塞拜疆", "MA": "摩洛哥", "VE": "委内瑞拉",
}

// 常用地区排在前面，其余按节点数量和代码排序
var countryOrder = []string{"HK", "TW", "JP", "SG", "KR", "US", "GB", "DE", "MO", "CN"}

func countryFlag(code string) string {
	if len(code) != 2 {
		return ""
	}
	return string([]rune{rune(code[0]-'A') + 0x1F1E6, rune(code[1]-'A') + 0x1F1E6})
}

func countryLabel(code, fallback string) string {
	if name := countryNames[code]; name != "" {
		return name
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" && safeScalar(fallback) && !strings.Contains(fallback, ",") {
		return fallback
	}
	return code
}

// wantsCountryGroups 只有开启自动国家分组、且包含全部节点的手动选择组才会挂载国家分组
func wantsCountryGroup(g RuleGroup) bool {
	return g.CountryGroups && g.IncludeAll && g.Type == "select"
}

// planCountryGroups 为节点中出现的每个国家生成分组名，并登记到 reserved 避免与节点重名
func planCountryGroups(nodes []Node, reserved map[string]bool) []countryGroup {
	counts, fallback := map[string]int{}, map[string]string{}
	for _, n := range nodes {
		code := strings.ToUpper(strings.TrimSpace(n.CountryCode))
		if len(code) != 2 || code == "CF" || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
			continue
		}
		counts[code]++
		if fallback[code] == "" {
			fallback[code] = n.Country
		}
	}
	rank := map[string]int{}
	for i, code := range countryOrder {
		rank[code] = i + 1
	}
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool {
		a, b := codes[i], codes[j]
		if (rank[a] > 0) != (rank[b] > 0) {
			return rank[a] > 0
		}
		if rank[a] > 0 {
			return rank[a] < rank[b]
		}
		if counts[a] != counts[b] {
			return counts[a] > counts[b]
		}
		return a < b
	})
	out := make([]countryGroup, 0, len(codes))
	for _, code := range codes {
		base := countryFlag(code) + " " + countryLabel(code, fallback[code])
		name := base
		for i := 2; reserved[name]; i++ {
			name = fmt.Sprintf("%s %d", base, i)
		}
		reserved[name] = true
		out = append(out, countryGroup{Code: code, Name: name})
	}
	return out
}

// countryProxyGroups 输出国家分组定义，去掉没有成功导出节点的国家
func countryProxyGroups(plan []countryGroup, nodes []Node, names []string) ([]string, []map[string]any) {
	refs, defs := []string{}, []map[string]any{}
	for _, cg := range plan {
		members := []string{}
		for i, n := range nodes {
			if strings.ToUpper(n.CountryCode) == cg.Code {
				members = append(members, names[i])
			}
		}
		if len(members) == 0 {
			continue
		}
		refs = append(refs, cg.Name)
		defs = append(defs, map[string]any{"name": cg.Name, "type": "url-test", "url": "https://www.gstatic.com/generate_204", "interval": 300, "tolerance": 50, "proxies": members})
	}
	return refs, defs
}
