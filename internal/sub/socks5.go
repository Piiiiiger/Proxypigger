package sub

import (
	"cmp"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// Socks5ForClient lists a user's SOCKS5 logins at the address and port a share link
// would advertise, host being the one the request reached; subscriptions never carry them.
func Socks5ForClient(host, email string) ([]service.Socks5Proxy, error) {
	clients := service.ClientService{}
	rec, err := clients.GetRecordByEmail(nil, email)
	if err != nil {
		return nil, err
	}
	ids, err := clients.GetInboundIdsForRecord(rec.Id)
	if err != nil {
		return nil, err
	}
	out := []service.Socks5Proxy{}
	if len(ids) == 0 || rec.Password == "" {
		return out, nil
	}
	db := database.GetDB()
	var inbounds []*model.Inbound
	if err := db.Where("id IN ? AND protocol = ?", ids, model.Mixed).
		Order("sub_sort_index ASC").Order("id ASC").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	exits := map[int]string{}
	var chains []model.ProxyChain
	if err := db.Where("enabled = ?", true).Find(&chains).Error; err != nil {
		return nil, err
	}
	for _, chain := range chains {
		var target model.Inbound
		if err := db.Select("remark", "tag").First(&target, chain.TargetInboundId).Error; err == nil {
			exits[chain.RelayInboundId] = cmp.Or(strings.TrimSpace(target.Remark), target.Tag)
		}
	}
	req := NewSubService("").ForRequest(host)
	for _, ib := range inbounds {
		endpoints := req.advertisedEndpoints(req.withPublicPort(ib))
		if len(endpoints) == 0 {
			continue
		}
		out = append(out, service.Socks5Proxy{
			InboundId: ib.Id, Remark: cmp.Or(strings.TrimSpace(ib.Remark), ib.Tag),
			Host: endpoints[0].Address, Port: endpoints[0].Port,
			Username: rec.Email, Password: rec.Password, Exit: exits[ib.Id],
			Enable: ib.Enable && rec.Enable,
		})
	}
	return out, nil
}
