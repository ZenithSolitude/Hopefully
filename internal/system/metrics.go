package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Metrics struct {
	CPU       float64 `json:"cpu"`
	MemUsed   uint64  `json:"mem_used"`
	MemTotal  uint64  `json:"mem_total"`
	MemPct    float64 `json:"mem_pct"`
	DiskUsed  uint64  `json:"disk_used"`
	DiskTotal uint64  `json:"disk_total"`
	DiskPct   float64 `json:"disk_pct"`
	Uptime    string  `json:"uptime"`
	LoadAvg   string  `json:"load_avg"`
}

func (m *Metrics) MemUsedStr() string  { return fmtBytes(m.MemUsed) }
func (m *Metrics) MemTotalStr() string { return fmtBytes(m.MemTotal) }
func (m *Metrics) DiskUsedStr() string { return fmtBytes(m.DiskUsed) }
func (m *Metrics) DiskTotalStr() string { return fmtBytes(m.DiskTotal) }
func (m *Metrics) CPUStr() string      { return fmt.Sprintf("%.1f", m.CPU) }
func (m *Metrics) MemPctStr() string   { return fmt.Sprintf("%.1f", m.MemPct) }
func (m *Metrics) DiskPctStr() string  { return fmt.Sprintf("%.1f", m.DiskPct) }

func Get() *Metrics {
	m := &Metrics{}
	m.CPU = cpuPct()
	m.MemUsed, m.MemTotal, m.MemPct = memInfo()
	m.DiskUsed, m.DiskTotal, m.DiskPct = diskInfo()
	m.Uptime = uptime()
	m.LoadAvg = loadAvg()
	return m
}

// cpuPct — читает /proc/stat дважды с паузой 250ms.
func cpuPct() float64 {
	s1 := readStat()
	time.Sleep(250 * time.Millisecond)
	s2 := readStat()
	if len(s1) < 5 || len(s2) < 5 {
		return 0
	}
	total := func(s []uint64) (total, idle uint64) {
		for _, v := range s {
			total += v
		}
		return total, s[3]
	}
	t1, i1 := total(s1)
	t2, i2 := total(s2)
	dt := float64(t2 - t1)
	if dt == 0 {
		return 0
	}
	return (1 - float64(i2-i1)/dt) * 100
}

func readStat() []uint64 {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		vals := make([]uint64, len(fields))
		for i, s := range fields {
			vals[i], _ = strconv.ParseUint(s, 10, 64)
		}
		return vals
	}
	return nil
}

func memInfo() (used, total uint64, pct float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	kv := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) >= 2 {
			v, _ := strconv.ParseUint(parts[1], 10, 64)
			kv[strings.TrimSuffix(parts[0], ":")] = v * 1024
		}
	}
	total = kv["MemTotal"]
	avail := kv["MemAvailable"]
	if total == 0 {
		return
	}
	used = total - avail
	pct = float64(used) / float64(total) * 100
	return
}

func diskInfo() (used, total uint64, pct float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return
	}
	total = st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	used = total - free
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	return
}

func uptime() string {
	data, _ := os.ReadFile("/proc/uptime")
	if len(data) == 0 {
		return "?"
	}
	secs, _ := strconv.ParseFloat(strings.Fields(string(data))[0], 64)
	d := time.Duration(secs) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dд %dч %dм", days, hours, mins)
	}
	return fmt.Sprintf("%dч %dм", hours, mins)
}

func loadAvg() string {
	data, _ := os.ReadFile("/proc/loadavg")
	if len(data) == 0 {
		return "?"
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fields[0] + " " + fields[1] + " " + fields[2]
	}
	return "?"
}

func fmtBytes(b uint64) string {
	const u = 1024
	if b < u {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(u), 0
	for n := b / u; n >= u; n /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
