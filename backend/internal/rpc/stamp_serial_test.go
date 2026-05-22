package rpc

import "testing"

func TestParseAndFormatStampSerial(t *testing.T) {
	cases := []struct {
		in     string
		prefix string
		num    int64
		pad    int
	}{
		{"ABC00001", "ABC", 1, 5},
		{"ABC00100", "ABC", 100, 5},
		{"ABCD000001", "ABCD", 1, 6},
		{"NOPREFIX1234", "NOPREFIX", 1234, 4},
		{"NOPREFIX0001", "NOPREFIX", 1, 4},
		{"NONUMBER", "NONUMBER", 0, 0},
		{"", "", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			prefix, num, pad := parseStampSerial(c.in)
			if prefix != c.prefix {
				t.Errorf("prefix: got %q want %q", prefix, c.prefix)
			}
			if num != c.num {
				t.Errorf("num: got %d want %d", num, c.num)
			}
			if pad != c.pad {
				t.Errorf("pad: got %d want %d", pad, c.pad)
			}
			if pad > 0 {
				out := formatStampSerial(prefix, num, pad)
				if out != c.in {
					t.Errorf("round-trip: got %q want %q", out, c.in)
				}
			}
		})
	}
}

func TestComputeStampRange(t *testing.T) {
	cases := []struct {
		name             string
		orderStart       string
		priorApplied     int32
		take             int32
		wantStart, wantEnd string
	}{
		{"first usage from a fresh order", "ABC00001", 0, 100, "ABC00001", "ABC00100"},
		{"second usage continues sequence", "ABC00001", 100, 50, "ABC00101", "ABC00150"},
		{"single bottle range", "ABC00050", 10, 1, "ABC00060", "ABC00060"},
		{"empty order start → empty range", "", 0, 100, "", ""},
		{"zero take → empty range", "ABC00001", 0, 0, "", ""},
		{"unparseable serial → empty", "NONUMERIC", 0, 100, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, e := computeStampRange(c.orderStart, c.priorApplied, c.take)
			if s != c.wantStart {
				t.Errorf("start: got %q want %q", s, c.wantStart)
			}
			if e != c.wantEnd {
				t.Errorf("end: got %q want %q", e, c.wantEnd)
			}
		})
	}
}
