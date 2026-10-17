package server

import (
	"bytes"
	"html/template"
	"strings"
	"time"
)

// mailContent 同时携带纯文本和 HTML，客户端按能力选择
type mailContent struct {
	Subject string
	Text    string
	HTML    string
}

type mailRow struct{ Label, Value string }

type mailView struct {
	Title     string
	Preheader string
	Lead      string
	Code      string
	Rows      []mailRow
	Note      string
	Action    string
	ActionURL string
	Footer    string
	Year      int
}

const brandName = "MCII节点共享"

var beijing = time.FixedZone("CST", 8*3600)

var mailLayout = template.Must(template.New("mail").Parse(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><title>{{.Title}}</title></head>
<body style="margin:0;padding:0;background:#eef2f0;-webkit-text-size-adjust:100%;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Preheader}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#eef2f0;">
<tr><td align="center" style="padding:32px 14px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:520px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',Arial,sans-serif;">
<tr><td style="padding:0 4px 16px;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
<td width="34" height="34" align="center" valign="middle" style="width:34px;height:34px;border-radius:9px;background:#187653;color:#ffffff;font-size:18px;font-weight:700;line-height:34px;">M</td>
<td style="padding-left:10px;font-size:16px;font-weight:700;color:#14231c;">` + brandName + `</td>
</tr></table>
</td></tr>
<tr><td style="background:#ffffff;border:1px solid #dfe7e2;border-radius:14px;overflow:hidden;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
<tr><td style="height:4px;background:#187653;font-size:0;line-height:0;">&nbsp;</td></tr>
<tr><td style="padding:30px 32px 8px;">
<h1 style="margin:0 0 10px;font-size:21px;line-height:1.4;color:#14231c;font-weight:700;">{{.Title}}</h1>
<p style="margin:0;font-size:14px;line-height:1.75;color:#4a5a52;">{{.Lead}}</p>
</td></tr>
{{if .Code}}<tr><td style="padding:20px 32px 4px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0"><tr>
<td align="center" style="background:#edf6f0;border:1px dashed #9fcdb5;border-radius:12px;padding:20px 12px;">
<div style="font-size:12px;letter-spacing:2px;color:#5f7a6c;margin-bottom:8px;">验证码</div>
<div style="font-family:'SFMono-Regular',Consolas,'Liberation Mono',Menlo,monospace;font-size:34px;font-weight:700;letter-spacing:10px;color:#146147;padding-left:10px;">{{.Code}}</div>
</td></tr></table>
</td></tr>{{end}}
{{if .Rows}}<tr><td style="padding:18px 32px 4px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="border:1px solid #e4ebe7;border-radius:10px;border-collapse:separate;">
{{range $i, $r := .Rows}}<tr>
<td style="padding:11px 14px;font-size:13px;color:#6b7c73;white-space:nowrap;vertical-align:top;{{if $i}}border-top:1px solid #edf1ee;{{end}}">{{$r.Label}}</td>
<td style="padding:11px 14px;font-size:13px;color:#14231c;line-height:1.6;word-break:break-all;{{if $i}}border-top:1px solid #edf1ee;{{end}}">{{$r.Value}}</td>
</tr>{{end}}
</table>
</td></tr>{{end}}
{{if .ActionURL}}<tr><td style="padding:22px 32px 4px;">
<a href="{{.ActionURL}}" style="display:inline-block;background:#187653;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:11px 22px;border-radius:9px;">{{.Action}}</a>
</td></tr>{{end}}
{{if .Note}}<tr><td style="padding:18px 32px 0;">
<p style="margin:0;font-size:12.5px;line-height:1.7;color:#7a8a82;">{{.Note}}</p>
</td></tr>{{end}}
<tr><td style="padding:26px 32px 28px;"><div style="height:1px;background:#edf1ee;font-size:0;line-height:0;">&nbsp;</div>
<p style="margin:14px 0 0;font-size:12px;line-height:1.7;color:#9aa8a1;">{{.Footer}}</p></td></tr>
</table>
</td></tr>
<tr><td align="center" style="padding:18px 4px 0;font-size:12px;color:#9aa8a1;">© {{.Year}} ` + brandName + ` · 此邮件由系统自动发送，请勿直接回复</td></tr>
</table>
</td></tr></table>
</body></html>`))

func renderMail(subject string, v mailView) mailContent {
	if v.Year == 0 {
		v.Year = time.Now().Year()
	}
	if v.Footer == "" {
		v.Footer = "如果不是你本人操作，请忽略这封邮件，你的账号不会有任何变化。"
	}
	var h bytes.Buffer
	if err := mailLayout.Execute(&h, v); err != nil {
		h.Reset()
	}
	var t strings.Builder
	t.WriteString(v.Title + "\n\n" + v.Lead + "\n")
	if v.Code != "" {
		t.WriteString("\n验证码：" + v.Code + "\n")
	}
	for _, r := range v.Rows {
		t.WriteString(r.Label + "：" + r.Value + "\n")
	}
	if v.ActionURL != "" {
		t.WriteString("\n" + v.Action + "：" + v.ActionURL + "\n")
	}
	if v.Note != "" {
		t.WriteString("\n" + v.Note + "\n")
	}
	t.WriteString("\n" + v.Footer + "\n\n—— " + brandName)
	return mailContent{Subject: "【" + brandName + "】" + subject, Text: t.String(), HTML: h.String()}
}

var codeTitles = map[string][2]string{
	"register": {"注册验证码", "欢迎加入" + brandName + "。请在注册页面输入下面的验证码，完成邮箱验证。"},
	"login":    {"登录验证码", "你正在使用邮箱验证码登录" + brandName + "，请在登录页面输入下面的验证码。"},
	"reset":    {"重置密码验证码", "你正在重置" + brandName + "的登录密码，请在页面中输入下面的验证码并设置新密码。"},
	"bind":     {"绑定邮箱验证码", "你正在为" + brandName + "账号绑定这个邮箱，请在页面中输入下面的验证码。"},
}

func codeMail(purpose, code string) mailContent {
	t := codeTitles[purpose]
	return renderMail(t[0], mailView{
		Title: t[0], Preheader: "你的验证码是 " + code + "，10 分钟内有效", Lead: t[1], Code: code,
		Note: "验证码 10 分钟内有效，最多可尝试 5 次。请勿把验证码告诉任何人，工作人员不会向你索要验证码。",
	})
}

func testMail(host string) mailContent {
	return renderMail("邮件服务测试", mailView{
		Title: "邮件服务配置成功", Preheader: "收到这封邮件说明 SMTP 配置可用", Lead: "这是一封测试邮件。能看到它，说明运行设置中的 SMTP 配置可以正常发信。",
		Rows:   []mailRow{{"发信服务器", host}, {"发送时间", time.Now().In(beijing).Format("2006-01-02 15:04:05") + "（北京时间）"}},
		Footer: "这封邮件由管理员在运行设置中手动触发。",
	})
}

// takedownMail 通知提供方来源被下架或恢复
func takedownMail(source, reason, detail, operator, link string, restored bool) mailContent {
	if restored {
		return renderMail("来源已恢复上架", mailView{
			Title: "你的来源已恢复上架", Preheader: "来源「" + source + "」已恢复", Lead: "管理员已恢复你的来源，订阅用户可以重新领取其中的节点。",
			Rows: []mailRow{{"来源", source}, {"操作人", operator}}, Action: "查看我的来源", ActionURL: link,
			Footer: "如有疑问，可在站内“用户反馈”联系管理员。",
		})
	}
	rows := []mailRow{{"来源", source}, {"下架原因", reason}}
	if detail != "" {
		rows = append(rows, mailRow{"补充说明", detail})
	}
	rows = append(rows, mailRow{"操作人", operator})
	return renderMail("来源已被下架", mailView{
		Title: "你的来源已被下架", Preheader: "来源「" + source + "」：" + reason, Lead: "管理员下架了你共享的来源。下架期间其中的节点不会出现在节点总览，也不会输出到任何订阅。",
		Rows: rows, Action: "查看我的来源", ActionURL: link,
		Note:   "如果问题已经处理，可以联系管理员申请恢复。",
		Footer: "如有疑问，可在站内“用户反馈”联系管理员。",
	})
}
