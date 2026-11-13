// Package terminal implements the client side of an interactive terminal link.
package terminal

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"sync"

	"tdev/internal/config"
)

const maxControlString = 1 << 20

// Output owns the outer title. The peer supplies only project identity and
// window alert state; the connection name never leaves this client.
type Output struct {
	mu      sync.Mutex
	out     io.Writer
	server  string
	project string
	target  string
	titles  bool
	parser  outputParser
}

func NewOutput(out io.Writer, server, project string, titles bool) *Output {
	o := &Output{out: out, server: server, project: project, target: project, titles: titles}
	o.parser.title = o.title
	return o
}

func (o *Output) title(value string) []byte {
	if !strings.HasPrefix(value, "tdev-alert-v1:") {
		return nil
	}
	project, marks, ok := strings.Cut(strings.TrimPrefix(value, "tdev-alert-v1:"), ":")
	if !ok || strings.Trim(marks, "1") != "" || (project != "" && !config.ValidName(project)) {
		return nil
	}
	if project != "" {
		o.project = project
	}
	return o.titleSequence(len(marks) > 0)
}

func (o *Output) titleSequence(pending bool) []byte {
	if !o.titles {
		return nil
	}
	prefix := ""
	if pending {
		prefix = "[!] "
	}
	return []byte("\x1b]2;" + prefix + "tdev: " + o.server + "/" + o.project + "\a")
}

// Start also discards any incomplete sequence from a previous connection.
func (o *Output) Start() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.parser = outputParser{title: o.title}
	o.project = o.target
	return writeOutput(o.out, o.titleSequence(false))
}

func writeOutput(w io.Writer, p []byte) error {
	if len(p) == 0 {
		return nil
	}
	n, err := w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return err
}

func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	filtered := o.parser.filter(p)
	if err := writeOutput(o.out, filtered); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Buffer only OSC and possible passthrough DCS strings. Other strings stream
// unchanged (including large graphics). A capped string is discarded through
// its terminator, never flushed as executable terminal bytes.
type outputParser struct {
	state    byte // 0: text, e: ESC, o: OSC, d: DCS, s: other string, a: other OSC
	buf      []byte
	escaped  bool
	discard  bool
	tmuxDCS  bool
	utf8Left int
	depth    int
	title    func(string) []byte
}

func (p *outputParser) reset() {
	p.state, p.escaped, p.discard, p.tmuxDCS = 0, false, false, false
	p.buf = p.buf[:0]
}

func (p *outputParser) filter(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		out = p.advance(out, b)
	}
	return out
}

func (p *outputParser) advance(out []byte, b byte) []byte {
	continuation := p.utf8Left > 0 && b&0xc0 == 0x80
	if continuation {
		p.utf8Left--
	} else {
		p.utf8Left = 0
		if b >= 0xc2 && b <= 0xf4 {
			if b < 0xe0 {
				p.utf8Left = 1
			} else if b < 0xf0 {
				p.utf8Left = 2
			} else {
				p.utf8Left = 3
			}
		}
	}
	switch p.state {
	case 0:
		// A UTF-8 continuation byte is not an eight-bit C1 control.
		if continuation {
			return append(out, b)
		}
		switch b {
		case 0x1b:
			p.state = 'e'
			return out
		case 0x9d:
			p.state, p.buf = 'o', append(p.buf[:0], b)
			return out
		case 0x90:
			p.state, p.buf = 'd', append(p.buf[:0], b)
			return out
		case 0x98, 0x9e, 0x9f:
			p.state = 's'
		}
		return append(out, b)
	case 'e':
		switch b {
		case ']':
			p.state, p.buf = 'o', append(p.buf[:0], 0x1b, b)
		case 'P':
			p.state, p.buf = 'd', append(p.buf[:0], 0x1b, b)
		case '_', '^', 'X':
			p.state = 's'
			return append(out, 0x1b, b)
		case 0x1b:
			return append(out, 0x1b)
		default:
			p.state = 0
			return append(out, 0x1b, b)
		}
		return out
	case 's', 'a':
		if p.escaped {
			if b == '\\' {
				p.reset()
				return append(out, 0x1b, b)
			}
			// A new escape aborts the string. Hold ESC until this decision so
			// a malformed clipboard/graphics string cannot hide a title OSC.
			p.reset()
			p.state = 'e'
			return p.advance(append(out, 0x18), b)
		}
		if !continuation && (b == 0x90 || b == 0x9d || b == 0x98 || b == 0x9e || b == 0x9f) {
			p.reset()
			return p.advance(append(out, 0x18), b)
		}
		if b == 0x1b {
			p.escaped = true
			return out
		}
		out = append(out, b)
		if b == 0x18 || b == 0x1a || (b == 0x9c && !continuation) || (p.state == 'a' && b == 7) {
			p.reset()
		}
		return out
	}
	// OSC/DCS, buffered until their kind is known or their terminator arrives.
	if !p.discard {
		p.buf = append(p.buf, b)
		if len(p.buf) > maxControlString {
			p.buf = p.buf[:0]
			p.discard = true
		}
	}
	if b == 0x18 || b == 0x1a {
		p.reset()
		return append(out, b)
	}
	if !continuation && (b == 0x90 || b == 0x9d || b == 0x98 || b == 0x9e || b == 0x9f) && p.state == 'o' {
		p.reset()
		return p.advance(out, b)
	}
	if p.state == 'd' && (bytes.HasPrefix(p.buf, []byte("\x1bPtmux;")) || bytes.HasPrefix(p.buf, []byte("\x90tmux;"))) {
		p.tmuxDCS = true
	}
	// In a tmux passthrough every payload ESC is doubled, including inner ST.
	if p.escaped && b == 0x1b && p.tmuxDCS {
		p.escaped = false
		return out
	}
	if (b == 0x9c && !continuation) || (p.escaped && b == '\\') || (p.state == 'o' && b == 7) {
		if !p.discard {
			out = append(out, p.complete()...)
		}
		p.reset()
		return out
	}
	if p.state == 'o' && p.escaped {
		// An ESC other than ST aborts OSC and starts a new escape sequence.
		p.reset()
		p.state = 'e'
		return p.advance(out, b)
	}
	p.escaped = b == 0x1b
	if p.discard {
		return out
	}
	prefix := 2
	if p.buf[0] != 0x1b {
		prefix = 1
	}
	payload := p.buf[prefix:]
	if !continuation && !p.tmuxDCS && len(payload) > 1 && payload[0] != 0x1b && payload[0] != 0x90 && payload[0] != 0x9d && (b == 0x90 || b == 0x9d || b == 0x98 || b == 0x9e || b == 0x9f) {
		p.reset()
		return p.advance(out, b)
	}
	if p.state == 'o' {
		if command, _, ok := bytes.Cut(payload, []byte(";")); ok {
			n, err := strconv.Atoi(string(command))
			if len(command) > 0 && (err != nil || n < 0 || n > 2) {
				out = append(out, p.buf...)
				p.buf = p.buf[:0]
				p.state = 'a'
			}
		}
	} else if p.state == 'd' && len(payload) > 0 && !bytes.HasPrefix([]byte("tmux;"), payload) && !p.tmuxDCS && payload[0] != 0x1b && payload[0] != 0x90 && payload[0] != 0x9d {
		out = append(out, p.buf...)
		if p.escaped {
			out = out[:len(out)-1]
		}
		p.buf = p.buf[:0]
		p.state = 's'
	}
	return out
}

func (p *outputParser) complete() []byte {
	prefix := 2
	if p.buf[0] != 0x1b {
		prefix = 1
	}
	end := len(p.buf) - 1
	if end > 0 && p.buf[end] == '\\' {
		end--
	}
	payload := p.buf[prefix:end]
	if p.state == 'o' {
		command, value, ok := bytes.Cut(payload, []byte(";"))
		n, err := strconv.Atoi(string(command))
		if (err == nil && n >= 0 && n <= 2) || len(command) == 0 {
			if ok && p.title != nil {
				return p.title(string(value))
			}
			return nil
		}
		return p.buf
	}
	wrapped := bytes.HasPrefix(payload, []byte("tmux;"))
	if wrapped {
		payload = bytes.ReplaceAll(payload[5:], []byte("\x1b\x1b"), []byte("\x1b"))
	}
	if !wrapped && (len(payload) == 0 || (payload[0] != 0x1b && payload[0] != 0x90 && payload[0] != 0x9d)) {
		return p.buf
	}
	if p.depth >= 8 {
		return nil
	}
	// Wrapped programs cannot contribute tdev state: strip their title commands.
	inner := outputParser{depth: p.depth + 1}
	filtered := inner.filter(payload)
	if len(filtered) == 0 {
		return nil
	}
	if wrapped {
		filtered = bytes.ReplaceAll(filtered, []byte("\x1b"), []byte("\x1b\x1b"))
		filtered = append([]byte("tmux;"), filtered...)
	}
	out := append([]byte(nil), p.buf[:prefix]...)
	out = append(out, filtered...)
	return append(out, p.buf[end:]...)
}
