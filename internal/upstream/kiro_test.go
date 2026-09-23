package upstream

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

// esHeader 构造一个 AWS Event Stream 字符串头（(len:1)(name)(type:1=string)(vlen:2)(value)）
func esHeader(name, val string) []byte {
	b := []byte{byte(len(name))}
	b = append(b, name...)
	b = append(b, 7)
	l := make([]byte, 2)
	binary.BigEndian.PutUint16(l, uint16(len(val)))
	b = append(b, l...)
	return append(b, val...)
}

// buildESFrame 构造完整帧（(total:4)(headersLen:4)(preludeCRC:4)(headers)(payload)(msgCRC:4)）
// 解析器不校验 CRC，此处填 0 占位——保持帧长与前缀语义真实。
func buildESFrame(eventType, messageType, payload string) []byte {
	var h []byte
	h = append(h, esHeader(":event-type", eventType)...)
	if messageType != "" {
		h = append(h, esHeader(":message-type", messageType)...)
	}
	out := make([]byte, 12)
	binary.BigEndian.PutUint32(out[0:4], uint32(12+len(h)+len(payload)+4))
	binary.BigEndian.PutUint32(out[4:8], uint32(len(h)))
	out = append(out, h...)
	out = append(out, payload...)
	return append(out, 0, 0, 0, 0)
}

func TestParseKiroCred(t *testing.T) {
	ok := `{"client_id":"cid","client_secret":"cs","refresh_token":"rt"}`
	c, err := parseKiroCred(ok)
	if err != nil {
		t.Fatalf("合法凭证应解析成功: %v", err)
	}
	if c.ClientID != "cid" || c.ClientSecret != "cs" || c.RefreshToken != "rt" {
		t.Fatalf("字段解析错误: %+v", c)
	}
	if c.Region != "us-east-1" {
		t.Fatalf("region 缺省应为 us-east-1，实际 %q", c.Region)
	}
	if _, err := parseKiroCred(`{"client_id":"cid"}`); err == nil {
		t.Fatal("缺 refresh_token 应报错")
	}
	if _, err := parseKiroCred(`not-json`); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

func TestChatToKiro_SystemHistoryCurrent(t *testing.T) {
	body := []byte(`{
	  "model":"claude-sonnet-4.5",
	  "messages":[
	    {"role":"system","content":"You are terse."},
	    {"role":"user","content":"My name is Bob."},
	    {"role":"assistant","content":"Hi Bob."},
	    {"role":"user","content":"What is my name?"}
	  ]}`)
	out, err := chatToKiro(body, "conv-1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	var m struct {
		ConversationState struct {
			ChatTriggerType string `json:"chatTriggerType"`
			ConversationID  string `json:"conversationId"`
			History         []struct {
				UserInputMessage *struct {
					Content string `json:"content"`
					ModelID string `json:"modelId"`
					Origin  string `json:"origin"`
				} `json:"userInputMessage"`
				AssistantResponseMessage *struct {
					Content string `json:"content"`
				} `json:"assistantResponseMessage"`
			} `json:"history"`
			CurrentMessage struct {
				UserInputMessage struct {
					Content string `json:"content"`
					ModelID string `json:"modelId"`
					Origin  string `json:"origin"`
				} `json:"userInputMessage"`
			} `json:"currentMessage"`
		} `json:"conversationState"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("产出不是合法 JSON: %v", err)
	}
	cs := m.ConversationState
	if cs.ChatTriggerType != "MANUAL" || cs.ConversationID != "conv-1" {
		t.Fatalf("会话头错误: %+v", cs)
	}
	if cs.CurrentMessage.UserInputMessage.Content != "You are terse.\n\nWhat is my name?" {
		t.Fatalf("system 应并入 currentMessage 头部，实际 %q", cs.CurrentMessage.UserInputMessage.Content)
	}
	if cs.CurrentMessage.UserInputMessage.ModelID != "claude-sonnet-4.5" ||
		cs.CurrentMessage.UserInputMessage.Origin != "KIRO_CLI" {
		t.Fatalf("currentMessage 的 modelId/origin 错误: %+v", cs.CurrentMessage.UserInputMessage)
	}
	if len(cs.History) != 2 {
		t.Fatalf("history 应有 2 轮，实际 %d", len(cs.History))
	}
	if cs.History[0].UserInputMessage == nil || cs.History[0].UserInputMessage.Content != "My name is Bob." {
		t.Fatalf("history[0] 应为 user=My name is Bob.，实际 %+v", cs.History[0])
	}
	if cs.History[1].AssistantResponseMessage == nil || cs.History[1].AssistantResponseMessage.Content != "Hi Bob." {
		t.Fatalf("history[1] 应为 assistant=Hi Bob.，实际 %+v", cs.History[1])
	}
}

func TestChatToKiro_RejectsImage(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4.5","messages":[{"role":"user","content":[
	  {"type":"text","text":"what is this"},
	  {"type":"image_url","image_url":{"url":"data:image/png;base64,AAA"}}]}]}`)
	if _, err := chatToKiro(body, "c"); err == nil {
		t.Fatal("图片输入应报错（本线 no_vision）")
	}
}

func TestChatToKiro_NoUserMessage(t *testing.T) {
	// 异常请求（只有 assistant）：不能产出空 content，否则上游必然 400
	body := []byte(`{"model":"auto","messages":[{"role":"assistant","content":"prev"}]}`)
	out, err := chatToKiro(body, "c")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if !bytes.Contains(out, []byte(`"content":"prev"`)) {
		t.Fatalf("无 user 消息时应把已有内容兜底进 currentMessage: %s", out)
	}
}

func TestAWSESReader_ParsesRealFrameShape(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(buildESFrame("initial-response", "event", `{"conversationId":"abc"}`))
	buf.Write(buildESFrame("assistantResponseEvent", "event", `{"content":"hello","modelId":"auto"}`))
	buf.Write(buildESFrame("meteringEvent", "event", `{"unit":"credit","usage":0.011}`))

	er := newAWSESReader(&buf)
	var types []string
	var contents []string
	for {
		f, err := er.next()
		if err != nil {
			break
		}
		types = append(types, f.EventType)
		if f.EventType == "assistantResponseEvent" {
			var v struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(f.Payload, &v)
			contents = append(contents, v.Content)
		}
	}
	want := []string{"initial-response", "assistantResponseEvent", "meteringEvent"}
	if len(types) != len(want) {
		t.Fatalf("帧数应为 %d，实际 %d（%v）", len(want), len(types), types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("第 %d 帧 event-type 应为 %s，实际 %s", i, want[i], types[i])
		}
	}
	if len(contents) != 1 || contents[0] != "hello" {
		t.Fatalf("payload 解析错误: %v", contents)
	}
}

func TestKiroPipeStream_EmitsOpenAISSE(t *testing.T) {
	var up bytes.Buffer
	up.Write(buildESFrame("initial-response", "event", `{"conversationId":"c1"}`))
	up.Write(buildESFrame("assistantResponseEvent", "event", `{"content":"Hel","modelId":"auto"}`))
	up.Write(buildESFrame("assistantResponseEvent", "event", `{"content":"lo","modelId":"auto"}`))
	up.Write(buildESFrame("meteringEvent", "event", `{"unit":"credit","usage":0.01}`))

	var out bytes.Buffer
	text, finish, err := kiroPipeStream(&up, &out, 7, "auto")
	if err != nil {
		t.Fatalf("转换不应报错: %v", err)
	}
	if text != "Hello" {
		t.Fatalf("聚合文本应为 Hello，实际 %q", text)
	}
	if finish != "stop" {
		t.Fatalf("finish 应为 stop，实际 %q", finish)
	}
	s := out.String()
	if !strings.Contains(s, `"role":"assistant"`) {
		t.Fatal("应首帧下发 role=assistant")
	}
	if !strings.Contains(s, `"content":"Hel"`) || !strings.Contains(s, `"content":"lo"`) {
		t.Fatalf("应逐帧下发内容增量:\n%s", s)
	}
	if !strings.Contains(s, `"finish_reason":"stop"`) {
		t.Fatal("末帧应带 finish_reason=stop")
	}
	if !strings.Contains(s, `"completion_tokens":2`) { // len("Hello")/4 = 1 → 下限 1；"Hello"=5 → 1
		// 5/4 = 1，故断言 usage 存在即可（精确值随估算口径变化）
		if !strings.Contains(s, `"completion_tokens"`) {
			t.Fatalf("末帧应带 usage:\n%s", s)
		}
	}
	if !strings.HasSuffix(s, "data: [DONE]\n\n") {
		t.Fatal("应以 [DONE] 收尾")
	}
}

func TestKiroPipeStream_EmptyUpstreamIsError(t *testing.T) {
	var up bytes.Buffer // 一个帧都没有
	var out bytes.Buffer
	_, finish, err := kiroPipeStream(&up, &out, 1, "auto")
	if err == nil {
		t.Fatal("上游无任何内容应报错（不能伪装成空回复成功计费）")
	}
	if finish != "error" {
		t.Fatalf("finish 应为 error，实际 %q", finish)
	}
	if !strings.Contains(out.String(), "kiro 上游未返回任何内容") {
		t.Fatalf("应下发明确错误帧:\n%s", out.String())
	}
}

func TestKiroEstTokens(t *testing.T) {
	if kiroEstTokens("") != 1 {
		t.Fatal("空文本应按 1 计（防 0 计费）")
	}
	if kiroEstTokens(strings.Repeat("a", 40)) != 10 {
		t.Fatal("40 字节应按 10 计")
	}
	if kiroPromptTokens(nil) != 1 {
		t.Fatal("空请求体应按 1 计")
	}
}
