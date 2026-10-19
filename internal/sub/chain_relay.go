package sub

import (
	"fmt"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// ChainRelayOutbound is the outbound a SOCKS5 relay dials its chain exit with: the
// user's own JSON-subscription proxy, with fixed REALITY picks so the config is stable.
func ChainRelayOutbound(target *model.Inbound, client model.Client) (map[string]any, error) {
	var settings service.SettingService
	mux, _ := settings.GetSubJsonMux()
	finalMask, _ := settings.GetSubJsonFinalMask()
	base := NewSubService("")
	req := base.ForRequest("")
	js := NewSubJsonService(mux, "", finalMask, "", base)
	js.pick = func(int) int { return 0 }
	inbound := *target
	req.projectThroughFallbackMaster(&inbound)
	configs := js.getConfig(req, req.withPublicPort(&inbound), client, "")
	if len(configs) == 0 {
		return nil, fmt.Errorf("chain exit %s has no client outbound", target.Remark)
	}
	out := extractProxyOutbound(configs[0])
	if out == nil {
		return nil, fmt.Errorf("chain exit %s (%s) cannot be dialed by a SOCKS5 relay", target.Remark, target.Protocol)
	}
	return out, nil
}
