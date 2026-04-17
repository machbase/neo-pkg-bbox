package config

import "net"

type MediamtxConfig struct {
	Binary         string `yaml:"binary" json:"binary"`                     // MediaMTX 바이너리 경로 (비어있으면 외부 서버 사용)
	ConfigFile     string `yaml:"config_file" json:"config_file"`           // MediaMTX 설정 파일 경로 (비어있으면 바이너리 디렉토리에서 자동 탐색)
	Host           string `yaml:"host" json:"host"`                         // MediaMTX 내부 API 호스트 (기본: 127.0.0.1)
	WebRTCHost     string `yaml:"webrtc_host" json:"webrtc_host"`           // 프론트에 노출할 WebRTC URL 호스트 (기본: 자동 감지된 서버 IP)
	Port           int    `yaml:"port" json:"port"`                         // MediaMTX HTTP API 포트 (기본: 9997)
	WebRTCPort     int    `yaml:"webrtc_port" json:"webrtc_port"`           // MediaMTX WebRTC 포트 (기본: 8889)
	RtspServerPort int    `yaml:"rtsp_server_port" json:"rtsp_server_port"` // MediaMTX RTSP 서버 포트 (기본: 8554)
}

// ApplyDefaults sets default values for MediamtxConfig
func (c *MediamtxConfig) ApplyDefaults() {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = 9997
	}
	if c.WebRTCPort == 0 {
		c.WebRTCPort = 8889
	}
	if c.RtspServerPort == 0 {
		c.RtspServerPort = 8554
	}
	if c.WebRTCHost == "" {
		c.WebRTCHost = detectOutboundIP()
		if c.WebRTCHost == "" {
			c.WebRTCHost = c.Host
		}
	}
}

// detectOutboundIP는 라우팅 테이블 기준으로 외부 통신에 사용되는 로컬 IP를 반환합니다.
// 실제 네트워크 연결은 하지 않으며, UDP 소켓 바인딩으로 로컬 주소만 확인합니다.
func detectOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
