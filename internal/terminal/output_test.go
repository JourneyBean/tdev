package terminal

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func wrapDCS(s string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(s, "\x1b", "\x1b\x1b") + "\x1b\\"
}

func TestOutputSequencesAndChunkBoundaries(t *testing.T) {
	base := "\x1b]2;tdev: dev-host/demo\a"
	pending := "\x1b]2;[!] tdev: dev-host/demo\a"
	clipboard := "\x1b]52;c;ZGVtbw==\a"
	link := "\x1b]8;;https://example.invalid\x1b\\link\x1b]8;;\x1b\\"
	for _, test := range []struct{ name, input, want string }{
		{"state", "\x1b]0;tdev-alert-v1:demo:11\a", pending},
		{"clear ST", "\x1b]2;tdev-alert-v1:demo:\x1b\\", base},
		{"switch managed session", "\x1b]0;tdev-alert-v1:scratch:1\a", "\x1b]2;[!] tdev: dev-host/scratch\a"},
		{"legacy missing project", "\x1b]0;tdev-alert-v1::1\a", pending},
		{"application titles", "a\x1b]0;app\ab\x1b]1;icon\x1b\\c\x1b]2;title\ad", "abcd"},
		{"numeric title", "\x1b]0002;app\a", ""},
		{"empty and incomplete commands", "\x1b]0\a\x1b]1\a\x1b]2\a\x1b];app\a", ""},
		{"BEL outside OSC", "\a\x1b]2;app\a\a", "\a\a"},
		{"non-title OSC", clipboard + link, clipboard + link},
		{"opaque DCS", "\x1bP1;2qgraphics\x1b\\", "\x1bP1;2qgraphics\x1b\\"},
		{"tmux wrapped title", wrapDCS("\x1b]2;app\a"), ""},
		{"screen wrapped title", "\x1bP\x1b]2;app\a\x1b\\", ""},
		{"wrapped ST title", wrapDCS("\x1b]2;app\x1b\\"), ""},
		{"wrapped clipboard", wrapDCS(clipboard), wrapDCS(clipboard)},
		{"mixed passthrough", wrapDCS("\x1b]2;app\a" + clipboard), wrapDCS(clipboard)},
		{"nested passthrough", wrapDCS(wrapDCS("\x1b]2;app\a" + clipboard)), wrapDCS(wrapDCS(clipboard))},
		{"wrapped state not accepted", wrapDCS("\x1b]0;tdev-alert-v1:other:1\a"), ""},
		{"invalid state", "\x1b]0;tdev-alert-v1:demo:12\a\x1b]2;tdev-alert-v1:../bad:1\a", ""},
		{"C1 OSC", "\x9d2;app\x9c\x9d0;tdev-alert-v1:demo:1\x9c", pending},
		{"UTF8 continuation", "中文🙂ННН", "中文🙂ННН"},
		{"UTF8 in title", "\x1b]2;Н🙂\aTEXT", "TEXT"},
		{"UTF8 non-title", "\x1b]8;;https://example.invalid/Н\a", "\x1b]8;;https://example.invalid/Н\a"},
		{"cancel", "\x1b]2;app\x18TEXT", "\x18TEXT"},
		{"interrupted OSC", "\x1b]2;unterminated\x1b]2;app\aTEXT", "TEXT"},
		{"interrupted clipboard", "\x1b]52;c;partial\x1b]2;app\aTEXT", "\x1b]52;c;partial\x18TEXT"},
		{"interrupted graphic", "\x1bPqpartial\x1b]2;app\aTEXT", "\x1bPqpartial\x18TEXT"},
		{"interrupted OSC C1", "\x1b]x\x9d2;app\aTEXT", "TEXT"},
		{"interrupted DCS C1", "\x1bPt\x9d2;app\aTEXT", "TEXT"},
		{"ANSI preserved", "\x1b[31mRED\x1b[0m", "\x1b[31mRED\x1b[0m"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for split := 0; split <= len(test.input); split++ {
				var out bytes.Buffer
				filter := NewOutput(&out, "dev-host", "demo", true)
				_, _ = filter.Write([]byte(test.input[:split]))
				_, _ = filter.Write([]byte(test.input[split:]))
				if out.String() != test.want {
					t.Fatalf("split %d: got %q want %q", split, out.String(), test.want)
				}
			}
			var out bytes.Buffer
			filter := NewOutput(&out, "dev-host", "demo", true)
			for i := range len(test.input) {
				_, _ = filter.Write([]byte{test.input[i]})
			}
			if out.String() != test.want {
				t.Fatalf("bytewise: got %q want %q", out.String(), test.want)
			}
		})
	}
}

func TestOutputLimitsReconnectAndRedirect(t *testing.T) {
	var out bytes.Buffer
	filter := NewOutput(&out, "dev-host", "demo", true)
	_, _ = filter.Write([]byte("\x1b]2;" + strings.Repeat("x", maxControlString+100) + "\aOK"))
	if out.String() != "OK" {
		t.Fatal("oversized title leaked")
	}
	out.Reset()
	// Other OSC and graphic DCS strings stream without the title buffer limit.
	other := "\x1b]52;c;" + strings.Repeat("a", maxControlString+100) + "\a\x1bPq" + strings.Repeat("a", maxControlString+100) + "\x1b\\"
	_, _ = filter.Write([]byte(other))
	if out.String() != other {
		t.Fatal("large non-title string damaged")
	}
	out.Reset()
	_, _ = filter.Write([]byte(wrapDCS("\x1b]2;"+strings.Repeat("x", maxControlString+100)+"\a") + "OK"))
	if out.String() != "OK" {
		t.Fatal("oversized passthrough leaked")
	}
	out.Reset()
	_, _ = filter.Write([]byte("\x1b]2;partial"))
	if err := filter.Start(); err != nil {
		t.Fatal(err)
	}
	_, _ = filter.Write([]byte("TEXT"))
	if out.String() != "\x1b]2;tdev: dev-host/demo\aTEXT" {
		t.Fatalf("reconnect: %q", out.String())
	}
	out.Reset()
	filter = NewOutput(&out, "dev-host", "demo", false)
	_ = filter.Start()
	_, _ = filter.Write([]byte("TEXT\x1b]0;tdev-alert-v1:demo:1\a\a"))
	if out.String() != "TEXT\a" {
		t.Fatalf("redirect: %q", out.String())
	}
}

type shortOutput struct{}

func (shortOutput) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestOutputWriteErrors(t *testing.T) {
	filter := NewOutput(shortOutput{}, "dev-host", "demo", true)
	if !errors.Is(filter.Start(), io.ErrShortWrite) {
		t.Fatal("initial title error lost")
	}
	if _, err := filter.Write([]byte("TEXT")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("output error lost")
	}
}

func FuzzOutputChunking(f *testing.F) {
	for _, seed := range []string{"中文🙂\a", "\x1b]2;app\a", wrapDCS("\x1b]2;app\x1b\\"), "\x1b]52;c;demo\a", "\x1b]0;tdev-alert-v1:demo:11\a"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 4096 {
			t.Skip()
		}
		var whole, chunked bytes.Buffer
		a, b := NewOutput(&whole, "dev-host", "demo", true), NewOutput(&chunked, "dev-host", "demo", true)
		_, _ = a.Write(input)
		for _, value := range input {
			_, _ = b.Write([]byte{value})
		}
		if !bytes.Equal(whole.Bytes(), chunked.Bytes()) {
			t.Fatal("output depends on transport chunking")
		}
	})
}
