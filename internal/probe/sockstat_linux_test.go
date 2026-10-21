//go:build linux

package probe

import (
	"encoding/binary"
	"testing"
	"time"
)

// diagMessage builds a synthetic inet_diag_msg for an IPv4 socket, optionally
// followed by an INET_DIAG_INFO attribute holding a tcp_info of infoLen bytes.
func diagMessage(src, dst [4]byte, sport, dport uint16, infoLen int, acked, received uint64, rttUs uint32) []byte {
	msg := make([]byte, sizeofDiagMsg)
	msg[0] = 2 // AF_INET
	msg[1] = 1 // TCP_ESTABLISHED
	binary.BigEndian.PutUint16(msg[4:6], sport)
	binary.BigEndian.PutUint16(msg[6:8], dport)
	copy(msg[8:12], src[:])
	copy(msg[24:28], dst[:])

	if infoLen == 0 {
		return msg
	}

	attr := make([]byte, 4+infoLen)
	binary.NativeEndian.PutUint16(attr[0:2], uint16(len(attr)))
	binary.NativeEndian.PutUint16(attr[2:4], inetDiagInfo)
	info := attr[4:]
	if infoLen >= tcpInfoMinLen {
		binary.NativeEndian.PutUint32(info[tcpInfoRTT:tcpInfoRTT+4], rttUs)
		binary.NativeEndian.PutUint64(info[tcpInfoBytesAcked:tcpInfoBytesAcked+8], acked)
		binary.NativeEndian.PutUint64(info[tcpInfoBytesReceived:tcpInfoBytesReceived+8], received)
	}
	return append(msg, attr...)
}

func TestParseDiagMessage(t *testing.T) {
	data := diagMessage([4]byte{10, 0, 0, 5}, [4]byte{8, 8, 8, 8}, 51000, 443, tcpInfoMinLen, 4096, 8192, 12500)

	key, st, ok := parseDiagMessage(data)
	if !ok {
		t.Fatal("parseDiagMessage returned ok=false for a well-formed message")
	}
	if want := "10.0.0.5:51000|8.8.8.8:443"; key != want {
		t.Errorf("key = %q, want %q", key, want)
	}
	if st.TxBytes != 4096 || st.RxBytes != 8192 {
		t.Errorf("counters = (tx %d, rx %d), want (4096, 8192)", st.TxBytes, st.RxBytes)
	}
	if st.RTT != 12500*time.Microsecond {
		t.Errorf("RTT = %s, want 12.5ms", st.RTT)
	}
}

// TIME_WAIT sockets and older kernels come back without the extension or with a
// shorter tcp_info; neither may be read past its length.
func TestParseDiagMessageRejectsIncomplete(t *testing.T) {
	full := [4]byte{10, 0, 0, 5}
	cases := map[string][]byte{
		"truncated msg":  make([]byte, sizeofDiagMsg-1),
		"no attributes":  diagMessage(full, full, 1, 2, 0, 0, 0, 0),
		"short tcp_info": diagMessage(full, full, 1, 2, tcpInfoMinLen-8, 1, 2, 3),
	}
	for name, data := range cases {
		if _, _, ok := parseDiagMessage(data); ok {
			t.Errorf("%s: expected ok=false", name)
		}
	}
}

// Attributes ahead of INET_DIAG_INFO must be skipped with correct 4-byte
// alignment, or the walk desynchronises and reads garbage.
func TestParseDiagInfoSkipsPrecedingAttrs(t *testing.T) {
	other := make([]byte, 4+6) // 10 bytes: pads to 12
	binary.NativeEndian.PutUint16(other[0:2], 10)
	binary.NativeEndian.PutUint16(other[2:4], 1) // INET_DIAG_MEMINFO

	info := make([]byte, 4+tcpInfoMinLen)
	binary.NativeEndian.PutUint16(info[0:2], uint16(len(info)))
	binary.NativeEndian.PutUint16(info[2:4], inetDiagInfo)
	binary.NativeEndian.PutUint64(info[4+tcpInfoBytesAcked:4+tcpInfoBytesAcked+8], 777)

	attrs := append(other, make([]byte, 2)...) // alignment padding
	attrs = append(attrs, info...)

	st, ok := parseDiagInfo(attrs)
	if !ok || st.TxBytes != 777 {
		t.Fatalf("parseDiagInfo = (%+v, %v), want TxBytes 777", st, ok)
	}
}

// A real dump must not error out on the machine running the tests, and must at
// least agree with itself about key formatting.
func TestSockStatsSmoke(t *testing.T) {
	stats, err := SockStats()
	if err != nil {
		t.Skipf("sock_diag unavailable: %v", err)
	}
	for key, st := range stats {
		if key == "" {
			t.Error("empty key in SockStats result")
		}
		if st.RTT < 0 {
			t.Errorf("%s: negative RTT %s", key, st.RTT)
		}
	}
}

// The quality fields live at higher tcp_info offsets than the byte counters,
// and the queues come from inet_diag_msg itself rather than the attribute.
func TestParseDiagMessageQualityFields(t *testing.T) {
	data := diagMessage([4]byte{10, 0, 0, 5}, [4]byte{8, 8, 8, 8}, 51000, 443, tcpInfoMinRTTLen, 4096, 8192, 12500)
	binary.NativeEndian.PutUint32(data[diagMsgRecvQ:diagMsgRecvQ+4], 128)
	binary.NativeEndian.PutUint32(data[diagMsgSendQ:diagMsgSendQ+4], 4096)

	info := data[sizeofDiagMsg+4:]
	binary.NativeEndian.PutUint32(info[tcpInfoTotalRetrans:tcpInfoTotalRetrans+4], 7)
	binary.NativeEndian.PutUint32(info[tcpInfoMinRTT:tcpInfoMinRTT+4], 2500)

	_, st, ok := parseDiagMessage(data)
	if !ok {
		t.Fatal("parseDiagMessage returned ok=false")
	}
	if st.Retrans != 7 {
		t.Errorf("Retrans = %d, want 7", st.Retrans)
	}
	if st.MinRTT != 2500*time.Microsecond {
		t.Errorf("MinRTT = %s, want 2.5ms", st.MinRTT)
	}
	if st.RecvQ != 128 || st.SendQ != 4096 {
		t.Errorf("queues = (rq %d, wq %d), want (128, 4096)", st.RecvQ, st.SendQ)
	}

}

// A pre-4.6 kernel sends a tcp_info without tcpi_min_rtt; the shorter payload
// must still yield the byte counters rather than being rejected or misread.
func TestParseDiagMessageOldKernelTcpInfo(t *testing.T) {
	data := diagMessage([4]byte{10, 0, 0, 5}, [4]byte{8, 8, 8, 8}, 1, 2, tcpInfoMinLen, 111, 222, 333)

	_, st, ok := parseDiagMessage(data)
	if !ok {
		t.Fatal("a short-but-valid tcp_info should still parse")
	}
	if st.TxBytes != 111 || st.RxBytes != 222 {
		t.Errorf("counters = (%d, %d), want (111, 222)", st.TxBytes, st.RxBytes)
	}
	if st.MinRTT != 0 {
		t.Errorf("MinRTT = %s, want zero when the kernel did not report it", st.MinRTT)
	}
}

// A listener is reported with its accept-queue depth in RecvQ and the backlog
// in SendQ, and must be returned even without tcp_info.
func TestParseDiagMessageListener(t *testing.T) {
	data := diagMessage([4]byte{0, 0, 0, 0}, [4]byte{0, 0, 0, 0}, 8080, 0, 0, 0, 0, 0)
	data[1] = tcpStateListen
	binary.NativeEndian.PutUint32(data[diagMsgRecvQ:], 12)
	binary.NativeEndian.PutUint32(data[diagMsgSendQ:], 128)

	key, st, ok := parseDiagMessage(data)
	if !ok {
		t.Fatal("ok = false for a listener without tcp_info")
	}
	if want := ListenKey("0.0.0.0:8080"); key != want {
		t.Errorf("key = %q, want %q", key, want)
	}
	if st.RecvQ != 12 || st.SendQ != 128 {
		t.Errorf("queue = %d/%d, want 12/128", st.RecvQ, st.SendQ)
	}
}
