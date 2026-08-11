package iperf

import (
	"testing"
)

func TestParseIPerf3TCP(t *testing.T) {
	parsed, err := ParseIPerf3([]byte(`{"end":{"sum_sent":{"bits_per_second":101000000,"retransmits":3},"sum_received":{"bits_per_second":99000000}}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.BitsPerSecond != 99_000_000 || parsed.Retransmits != 3 {
		t.Fatalf("parsed=%+v", parsed)
	}
}

func TestParseIPerf3UDP(t *testing.T) {
	parsed, err := ParseIPerf3([]byte(`{"end":{"sum":{"bits_per_second":8000000,"jitter_ms":0.25,"lost_percent":1.5}}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.BitsPerSecond != 8_000_000 || parsed.JitterMS != .25 || parsed.LostPercent != 1.5 {
		t.Fatalf("parsed=%+v", parsed)
	}
}

func TestParseIPerf2(t *testing.T) {
	parsed, err := ParseIPerf2([]byte("[SUM] 0.0-10.0 sec 1000000000 Bytes 800000000 bits/sec\n"), false)
	if err != nil || parsed.BitsPerSecond != 800_000_000 {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
	udp, err := ParseIPerf2([]byte("[  3] 0.0-10.0 sec 10000000 Bytes 8000000 bits/sec 0.125 ms 2/1000 (0.2%)\n"), true)
	if err != nil || udp.JitterMS != .125 || udp.LostPercent != .2 {
		t.Fatalf("udp=%+v err=%v", udp, err)
	}
}

func TestTargetArguments(t *testing.T) {
	arguments := targetArguments("example.com:5202")
	if len(arguments) != 4 || arguments[1] != "example.com" || arguments[3] != "5202" {
		t.Fatalf("arguments=%v", arguments)
	}
}
