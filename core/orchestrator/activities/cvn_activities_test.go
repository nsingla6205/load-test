package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	"github.com/stretchr/testify/assert"
	"go.temporal.io/sdk/testsuite"
)

// fakeCVN embeds the sdk.CVN interface so only the methods exercised by these
// tests need a real implementation; all other methods are unused.
type fakeCVN struct {
	sdk.CVN
	gotParams *sdk.DataPathParams
	resp      *sdk.DataPathResponse
	err       error
}

func (f *fakeCVN) CreateDataPathV1(params *sdk.DataPathParams) (*sdk.DataPathResponse, error) {
	f.gotParams = params
	return f.resp, f.err
}

func TestCVNActivity_CreateDataPath(t *testing.T) {
	params := sdk.DataPathParams{
		ZoneUUID:      "zone-uuid",
		MicrozoneUUID: "microzone-uuid",
		AccountUUID:   "account-uuid",
	}

	tests := []struct {
		name          string
		newCVNErr     error
		createResp    *sdk.DataPathResponse
		createErr     error
		expectErr     bool
		expectCalled  bool
		expectedVlan  int
		expectedRange string
	}{
		{
			name:          "success",
			createResp:    &sdk.DataPathResponse{SouthboundVlanID: 42, IPrangeUUID: "ip-range-uuid"},
			expectCalled:  true,
			expectedVlan:  42,
			expectedRange: "ip-range-uuid",
		},
		{
			name:      "client creation fails",
			newCVNErr: errors.New("CVN_HOST is not configured"),
			expectErr: true,
		},
		{
			name:         "create data path fails",
			createErr:    errors.New("cvn boom"),
			expectErr:    true,
			expectCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeCVN{resp: tt.createResp, err: tt.createErr}
			act := &CVNActivity{
				NewCVN: func(_ context.Context, accountID string) (sdk.CVN, error) {
					if tt.newCVNErr != nil {
						return nil, tt.newCVNErr
					}
					assert.Equal(t, params.AccountUUID, accountID)
					return fake, nil
				},
			}

			testSuite := &testsuite.WorkflowTestSuite{}
			env := testSuite.NewTestActivityEnvironment()
			env.RegisterActivity(act.CreateDataPath)

			val, err := env.ExecuteActivity(act.CreateDataPath, params)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				var resp sdk.DataPathResponse
				assert.NoError(t, val.Get(&resp))
				assert.Equal(t, tt.expectedVlan, resp.SouthboundVlanID)
				assert.Equal(t, tt.expectedRange, resp.IPrangeUUID)
			}

			if tt.expectCalled {
				assert.NotNil(t, fake.gotParams)
				assert.Equal(t, params.AccountUUID, fake.gotParams.AccountUUID)
			} else {
				assert.Nil(t, fake.gotParams)
			}
		})
	}
}
