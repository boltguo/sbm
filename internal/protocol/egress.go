package protocol

import "github.com/boltguo/sbm/internal/model"

func DirectAuthUser(in model.Inbound) string {
	return "direct-" + in.ID
}
