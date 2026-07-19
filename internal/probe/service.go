package probe

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
)

// builtinServices is a fallback map of "port/proto" -> service name for the most
// common ports, used when /etc/services is unavailable or lacks an entry.
var builtinServices = map[string]string{
	"20/tcp": "ftp-data", "21/tcp": "ftp", "22/tcp": "ssh", "23/tcp": "telnet",
	"25/tcp": "smtp", "53/tcp": "dns", "53/udp": "dns", "67/udp": "dhcp", "68/udp": "dhcp",
	"69/udp": "tftp", "80/tcp": "http", "110/tcp": "pop3", "123/udp": "ntp", "143/tcp": "imap",
	"161/udp": "snmp", "389/tcp": "ldap", "443/tcp": "https", "443/udp": "https",
	"445/tcp": "smb", "465/tcp": "smtps", "514/udp": "syslog", "587/tcp": "submission",
	"631/tcp": "ipp", "636/tcp": "ldaps", "993/tcp": "imaps", "995/tcp": "pop3s",
	"1194/udp": "openvpn", "1433/tcp": "mssql", "1521/tcp": "oracle", "3306/tcp": "mysql",
	"3389/tcp": "rdp", "5432/tcp": "postgres", "5900/tcp": "vnc", "6379/tcp": "redis",
	"8080/tcp": "http-alt", "8443/tcp": "https-alt", "9200/tcp": "elasticsearch",
	"11211/tcp": "memcached", "27017/tcp": "mongodb", "51820/udp": "wireguard",
}

var (
	svcOnce sync.Once
	svcMap  map[string]string // "port/proto" -> name
)

// loadServices populates svcMap from /etc/services (authoritative when present)
// then fills any gaps from builtinServices. Called once via svcOnce.
func loadServices() {
	svcMap = map[string]string{}

	if f, err := os.Open("/etc/services"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if i := strings.IndexByte(line, '#'); i >= 0 {
				line = line[:i]
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			portProto := strings.SplitN(fields[1], "/", 2)
			if len(portProto) != 2 {
				continue
			}
			key := portProto[0] + "/" + strings.ToLower(portProto[1])
			if _, ok := svcMap[key]; !ok {
				svcMap[key] = fields[0]
			}
		}
	}

	for k, v := range builtinServices {
		if _, ok := svcMap[k]; !ok {
			svcMap[k] = v
		}
	}
}

// ServiceName returns the well-known service name for a port/proto (e.g.
// 443/"tcp" -> "https"), or "" if the port is unknown. Backed by /etc/services
// when readable, with a built-in fallback for the most common ports.
func ServiceName(port int, proto string) string {
	if port <= 0 {
		return ""
	}
	svcOnce.Do(loadServices)
	return svcMap[strconv.Itoa(port)+"/"+strings.ToLower(proto)]
}
