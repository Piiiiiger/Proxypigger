package service

import "github.com/mhsanaei/3x-ui/v3/internal/util/common"

// Socks5Proxy is one SOCKS5 login of a user: where to connect, and the node the
// traffic leaves from when this one relays it on through a chain.
type Socks5Proxy struct {
	InboundId int    `json:"inboundId" example:"31"`
	Remark    string `json:"remark" example:"日本-NOSLA SOCKS5"`
	Host      string `json:"host" example:"198.51.100.10"`
	Port      int    `json:"port" example:"30001"`
	Username  string `json:"username" example:"alice"`
	Password  string `json:"password" example:"4f0c7c2d9a1b4e5f8a6b3c2d1e0f9a8b"`
	Exit      string `json:"exit" example:"日本-家宽"`
	Enable    bool   `json:"enable" example:"true"`
}

// Socks5Proxies lists a user's SOCKS5 logins; the subscription package resolves
// each node's public address, as it does for share links.
func (s *InboundService) Socks5Proxies(host, email string) ([]Socks5Proxy, error) {
	if email == "" {
		return nil, common.NewError("client email is required")
	}
	if registeredSubLinkProvider == nil {
		return nil, common.NewError("sub link provider not registered")
	}
	return registeredSubLinkProvider.Socks5ForClient(host, email)
}
