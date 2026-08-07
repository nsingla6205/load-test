package ontap_rest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/security"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

const securityAuditMessagesFields = "timestamp,node,user,application,input,state,message"

func (sc *securityClient) HasLoginPasswordResetSuccess(ctx context.Context, username string, since time.Time) (bool, error) {
	if username == "" {
		return false, errors.New("username is required to query login-password audit messages")
	}

	input := fmt.Sprintf("security login password -username %s", username)
	state := "success"

	timestamp := ">=" + since.UTC().Format(time.RFC3339)
	returnRecords := "false"

	params := security.NewSecurityAuditLogCollectionGetParamsWithContext(ctx)
	params.SetUser(&username)
	params.SetInput(&input)
	params.SetState(&state)
	params.SetTimestamp(&timestamp)
	params.SetReturnRecords(&returnRecords)
	params.SetFields(strings.Split(securityAuditMessagesFields, ","))

	response, err := (*sc.api).SecurityAuditLogCollectionGet(params, nil)
	if err != nil {
		return false, err
	}
	if response == nil || response.Payload == nil {
		return false, errors.New("unexpected response from SecurityAuditLogCollectionGet")
	}

	return nillable.FromPointer(response.Payload.NumRecords) > 0, nil
}
