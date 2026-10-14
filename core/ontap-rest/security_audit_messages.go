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

	// Match either interface used to change the password (ONTAP `|` OR operator):
	//   - CLI/SSH:             `security login password -username <user>`
	//   - REST/System Manager: `POST /api/security/authentication/password : [headers] {"name":"<user>","password":***}`
	// ONTAP logs the REST body as the client sent it: the `[headers]` part is only
	// present for some clients (e.g. System Manager), so a wildcard skips it. The
	// name must be compact JSON (`"name":"<user>"`, as System Manager sends it);
	// a client that sends `"name": "<user>"` is not matched. The quoted name
	// restricts the match to changes of this user's own password rather than any
	// account ociadmin changes.
	input := fmt.Sprintf(`security login password -username %[1]s|POST /api/security/authentication/password*"name":"%[1]s"*`, username)
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
