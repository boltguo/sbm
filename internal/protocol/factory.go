package protocol

import (
	"context"
	"fmt"

	"github.com/boltguo/sing-box/internal/model"
)

type Factory struct{ Keys KeyGenerator }

func (f Factory) New(ctx context.Context, kind, name string, port int) (model.Inbound, error) {
	id, err := RandomToken(12)
	if err != nil {
		return model.Inbound{}, err
	}
	in := model.Inbound{ID: id, Type: kind, Name: name, Enabled: true, Port: port}
	switch kind {
	case TypeVLESSReality:
		uuid, err := f.Keys.UUID(ctx)
		if err != nil {
			return model.Inbound{}, err
		}
		keys, err := f.Keys.Reality(ctx)
		if err != nil {
			return model.Inbound{}, err
		}
		shortID, err := RandomHex(8)
		if err != nil {
			return model.Inbound{}, err
		}
		in.VLESS = &model.VLESSOptions{UUID: uuid, SNI: "www.apple.com", PrivateKey: keys.Private, PublicKey: keys.Public, ShortID: shortID}
	case TypeHysteria2:
		password, err := RandomToken(24)
		if err != nil {
			return model.Inbound{}, err
		}
		in.Hysteria2 = &model.Hysteria2Options{Password: password}
	default:
		return model.Inbound{}, fmt.Errorf("不支持的协议类型 %q", kind)
	}
	return in, nil
}
