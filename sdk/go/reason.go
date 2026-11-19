package frontseat

import (
	"errors"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// Reason is why a server refused a call by name, as each call's contract
// lists it, and the domain of the service that refused: the
// google.rpc.ErrorInfo the refusal carries. A program reads the reason,
// never the message, which is for a person. Both are empty for an error
// that carries none.
//
//	if reason, _ := frontseat.Reason(err); reason == "WORKSPACE_NOT_FOUND" { ... }
func Reason(err error) (reason, domain string) {
	var refused *connect.Error
	if !errors.As(err, &refused) {
		return "", ""
	}
	for _, detail := range refused.Details() {
		message, err := connectproto.UnmarshalErrorDetail(detail)
		if err != nil {
			continue
		}
		if info, ok := message.(*errdetails.ErrorInfo); ok {
			return info.GetReason(), info.GetDomain()
		}
	}
	return "", ""
}
