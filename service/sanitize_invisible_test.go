package service

import (
	"bytes"
	"strings"
	"testing"
)

// TestOutboundStripsInvisibleMarks 锁定「无感」的底线：
// 入站脱敏插的零宽绝不能随响应流回客户端。
//
// 为什么这是无感的底线：模型读到 "s<ZWSP>andbox" 后若在回答或工具入参里复述，
// 零宽就会写进用户的源文件——肉眼看不见，但 diff 显示「有改动」、
// 字符串比较失败、正则匹配漏。这类问题极难排查，必须在出站统一兜掉。
func TestOutboundStripsInvisibleMarks(t *testing.T) {
	dirty := []byte(`data: {"choices":[{"delta":{"content":"run in the s` +
		"\u200b" + `andbox"}}]}` + "\n\n")
	clean := stripInvisibleFromFrame(dirty)

	if bytes.Contains(clean, []byte{0xe2, 0x80, 0x8b}) {
		t.Fatalf("零宽泄漏到出站字节: %q", clean)
	}
	if !strings.Contains(string(clean), "sandbox") {
		t.Fatalf("清理后文字应还原为 sandbox: %q", clean)
	}
	// 语义必须与原文一致（零宽只是不可见标记，剥离后应还原）
	if !strings.Contains(string(clean), "run in the sandbox") {
		t.Fatalf("剥零宽后语义不对: %q", clean)
	}
}

// TestSseSinkStripsInvisible 确认拦截点真的挂在流式出口上。
func TestSseSinkStripsInvisible(t *testing.T) {
	var buf bytes.Buffer
	sink := newSSESink(&buf, nil)

	frame := "event: delta\ndata: threat detected in sandbox mode\n\n"
	frame = strings.ReplaceAll(frame, "sandbox", "s"+"\u200b"+"andbox")
	if _, err := sink.Write([]byte(frame)); err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(buf.Bytes(), []byte{0xe2, 0x80, 0x8b}) {
		t.Fatalf("sseSink 未剥离零宽: %q", buf.Bytes())
	}
	if !strings.Contains(buf.String(), "sandbox mode") {
		t.Fatalf("sseSink 破坏了正常内容: %q", buf.String())
	}
}

// TestSseSinkReportsOriginalLength Write 的返回值必须是调用方给的长度，
// 否则上层的写入循环会误判短写。
func TestSseSinkReportsOriginalLength(t *testing.T) {
	var buf bytes.Buffer
	sink := newSSESink(&buf, nil)
	in := []byte("data: s\u200bandbox\n\n")
	n, err := sink.Write(in)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(in) {
		t.Fatalf("Write 应返回原始长度 %d，实际 %d", len(in), n)
	}
}

// TestCleanFrameUntouched 不含零宽的帧必须逐字节原样通过（不能有副作用）。
func TestCleanFrameUntouched(t *testing.T) {
	in := []byte("data: {\"content\":\"hello world\"}\n\n")
	out := stripInvisibleFromFrame(in)
	if !bytes.Equal(in, out) {
		t.Fatalf("干净帧被改动了: %q -> %q", in, out)
	}
}

// TestInvisibleMatrixCoversAllMarks 确认覆盖了所有可能混入的不可见字符。
func TestInvisibleMatrixCoversAllMarks(t *testing.T) {
	for _, s := range []string{"\u200b", "\u200c", "\u200d", "\ufeff"} {
		in := []byte("data: a" + s + "b")
		out := stripInvisibleFromFrame(in)
		if !bytes.Equal(out, []byte("data: ab")) {
			t.Fatalf("未剥离 %U: %q", []rune(s)[0], out)
		}
	}
}
