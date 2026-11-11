package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/ExpTechTW/proxygate/internal/subscription"
)

type localNode struct {
	SlotID     int    `json:"slotId"`
	Name       string `json:"name"`
	Country    string `json:"country"`
	SOCKS5Port int    `json:"socks5Port"`
	SOCKS5     string `json:"socks5"`
	password   string
}

type localPlan struct {
	BasePort    int         `json:"basePort"`
	Host        string      `json:"host"`
	Path        string      `json:"path"`
	MissingHost bool        `json:"missingHost"`
	Total       int         `json:"total"`
	Nodes       []localNode `json:"nodes"`
}

func (s *Server) localPlanHandler(c fiber.Ctx) error {
	plan, err := s.buildLocalPlan(parseLocalBasePort(c.Query("basePort")))
	if err != nil {
		return writeError(c, fiber.StatusServiceUnavailable, err.Error())
	}
	return writeJSON(c, fiber.StatusOK, plan)
}

func (s *Server) localShardx(c fiber.Ctx) error {
	plan, err := s.buildLocalPlan(parseLocalBasePort(c.Query("basePort")))
	if err != nil {
		return writeError(c, fiber.StatusServiceUnavailable, err.Error())
	}
	c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="shardx.txt"`)
	return c.SendString(localShardxList(plan))
}

func (s *Server) localSingbox(c fiber.Ctx) error {
	plan, err := s.buildLocalPlan(parseLocalBasePort(c.Query("basePort")))
	if err != nil {
		return writeError(c, fiber.StatusServiceUnavailable, err.Error())
	}
	config, err := localSingboxConfig(plan)
	if err != nil {
		return writeError(c, fiber.StatusBadRequest, err.Error())
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="proxygate-singbox.json"`)
	return c.Send(config)
}

func (s *Server) localBundle(c fiber.Ctx) error {
	plan, err := s.buildLocalPlan(parseLocalBasePort(c.Query("basePort")))
	if err != nil {
		return writeError(c, fiber.StatusServiceUnavailable, err.Error())
	}
	bundle, err := localBundleZip(plan)
	if err != nil {
		return writeError(c, fiber.StatusBadRequest, err.Error())
	}
	c.Set(fiber.HeaderContentType, "application/zip")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="proxygate-local.zip"`)
	return c.Send(bundle)
}

func (s *Server) buildLocalPlan(basePort int) (localPlan, error) {
	if s.matrix == nil || !s.matrix.Enabled() {
		return localPlan{}, fmt.Errorf("matrix is disabled")
	}
	settings := s.config.Get().Matrix
	host := strings.TrimSpace(settings.PublicHost)
	path := settings.SubscriptionTrojanPath
	if path == "" {
		path = "/pgtrojan"
	}
	token := settings.SubscriptionTokenValue()
	statuses, _ := s.matrix.Status()
	sort.Slice(statuses, func(left, right int) bool { return statuses[left].SlotID < statuses[right].SlotID })
	plan := localPlan{BasePort: basePort, Host: host, Path: path, MissingHost: host == "", Nodes: make([]localNode, 0, len(statuses))}
	for _, status := range statuses {
		if status.State != "ready" || !status.Healthy || status.ExitIP == "" {
			continue
		}
		port := basePort + status.SlotID - 1
		if port > 65535 {
			continue
		}
		plan.Nodes = append(plan.Nodes, localNode{
			SlotID:     status.SlotID,
			Name:       subscription.NodeName(status),
			Country:    subscription.NodeCountry(status),
			SOCKS5Port: port,
			SOCKS5:     fmt.Sprintf("socks5://127.0.0.1:%d", port),
			password:   subscription.SlotPassword(token, status.SlotID),
		})
	}
	if limit := settings.ShareLimit; limit > 0 && len(plan.Nodes) > limit {
		plan.Nodes = plan.Nodes[:limit]
	}
	plan.Total = len(plan.Nodes)
	return plan, nil
}

func parseLocalBasePort(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 12001
	}
	if value < 1024 {
		return 1024
	}
	if value > 65000 {
		return 65000
	}
	return value
}

func localShardxList(plan localPlan) string {
	var builder strings.Builder
	for _, node := range plan.Nodes {
		builder.WriteString(node.SOCKS5)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func localSingboxConfig(plan localPlan) ([]byte, error) {
	if plan.MissingHost {
		return nil, fmt.Errorf("public host is not configured; set the public host first")
	}
	inbounds := make([]map[string]any, 0, len(plan.Nodes))
	outbounds := make([]map[string]any, 0, len(plan.Nodes)+1)
	rules := make([]map[string]any, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		inTag := fmt.Sprintf("in-%02d", node.SlotID)
		inbounds = append(inbounds, map[string]any{
			"type":        "socks",
			"tag":         inTag,
			"listen":      "127.0.0.1",
			"listen_port": node.SOCKS5Port,
		})
		outbounds = append(outbounds, map[string]any{
			"type":        "trojan",
			"tag":         node.Name,
			"server":      plan.Host,
			"server_port": 443,
			"password":    node.password,
			"tls":         map[string]any{"enabled": true, "server_name": plan.Host},
			"transport": map[string]any{
				"type":    "ws",
				"path":    plan.Path,
				"headers": map[string]any{"Host": plan.Host},
			},
		})
		rules = append(rules, map[string]any{"inbound": []string{inTag}, "outbound": node.Name})
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	profile := map[string]any{
		"log":       map[string]any{"level": "warn"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     map[string]any{"rules": rules, "final": "direct"},
	}
	return json.MarshalIndent(profile, "", "  ")
}

func localBundleZip(plan localPlan) ([]byte, error) {
	config, err := localSingboxConfig(plan)
	if err != nil {
		return nil, err
	}
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	files := []struct {
		name string
		data []byte
	}{
		{"config.json", config},
		{"shardx.txt", []byte(localShardxList(plan))},
		{"start.cmd", []byte(localStartCmd(plan))},
		{"stop.cmd", []byte(localStopCmd())},
		{"README.txt", []byte(localReadme(plan))},
	}
	for _, file := range files {
		entry, err := writer.Create(file.name)
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func cmdLines(lines []string) string {
	return strings.Join(lines, "\r\n") + "\r\n"
}

func localStartCmd(plan localPlan) string {
	first, last := plan.BasePort, plan.BasePort
	if len(plan.Nodes) > 0 {
		first = plan.Nodes[0].SOCKS5Port
		last = plan.Nodes[len(plan.Nodes)-1].SOCKS5Port
	}
	return cmdLines([]string{
		"@echo off",
		"setlocal",
		`cd /d "%~dp0"`,
		`if not exist "sing-box.exe" (`,
		"  echo sing-box.exe not found in this folder.",
		"  echo Download sing-box v1.14.4 for Windows, unzip sing-box.exe here, then run start.cmd again:",
		"  echo https://ghfast.top/https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-windows-amd64.zip",
		"  pause",
		"  exit /b 1",
		")",
		`start "ProxyGate local nodes" /min sing-box.exe run -c config.json`,
		fmt.Sprintf("echo ProxyGate local SOCKS5 nodes: 127.0.0.1:%d - 127.0.0.1:%d", first, last),
		"echo In ShardX: Proxies - Add proxies - paste shardx.txt - SOCKS5 - Test all.",
		"timeout /t 3 >nul",
	})
}

func localStopCmd() string {
	return cmdLines([]string{
		"@echo off",
		"taskkill /IM sing-box.exe /F >nul 2>&1",
		"echo ProxyGate local nodes stopped.",
		"timeout /t 2 >nul",
	})
}

func localReadme(plan localPlan) string {
	first, last := plan.BasePort, plan.BasePort
	if len(plan.Nodes) > 0 {
		first = plan.Nodes[0].SOCKS5Port
		last = plan.Nodes[len(plan.Nodes)-1].SOCKS5Port
	}
	return strings.Join([]string{
		"ProxyGate-Matrix 本地接入包",
		"",
		"链路：ShardX -> 127.0.0.1:端口 (sing-box socks) -> " + plan.Host + plan.Path + " -> 槽位 -> 目标站",
		fmt.Sprintf("端口：%d - %d，共 %d 个节点。端口 = 起始端口 + 槽位号 - 1，例如 pg-07 固定用 起始端口+6。", first, last, plan.Total),
		"槽位掉线时只会在区间里留空档，不会把别的国家挪到已有端口上。",
		"",
		"使用步骤：",
		"1. 下载 sing-box v1.14.4 Windows 版（start.cmd 里也有链接），把 sing-box.exe 解压到本目录。",
		"2. 双击 start.cmd，保持窗口最小化即可。",
		"3. ShardX -> Proxies -> Add proxies，粘贴 shardx.txt 全部行，Type 选 SOCKS5，Preview -> Test all -> Add。",
		"4. 不用时双击 stop.cmd。",
		"",
		"说明：",
		"- 只有 TCP。sing-box 的 socks 入站不支持 UDP，ShardX 会自动切换 TCP-only。",
		"- 节点变了以后，回到面板分享页重新下载这个包即可（配置里的密码和槽位一一对应）。",
		"- 端口只监听 127.0.0.1，外部访问不到。",
		"",
	}, "\r\n")
}
