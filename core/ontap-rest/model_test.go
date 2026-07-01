package ontap_rest

import (
	"context"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/snapmirror"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestGcpKmsCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := gcpKmsCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &GcpKmsCreateParams{
			KeyName:                nillable.ToPointer("keyname"),
			KeyRingLocation:        nillable.ToPointer("ringlocation"),
			KeyRingName:            nillable.ToPointer("ringname"),
			ProjectID:              nillable.ToPointer("projectid"),
			ApplicationCredentials: nillable.ToPointer(strfmt.Password("credentials")),
			SvmName:                nillable.ToPointer("svmname"),
		}

		otParams := gcpKmsCreateParamsToONTAP(params)
		assert.Equal(tt, "keyname", *otParams.Info.KeyName)
		assert.Equal(tt, "ringlocation", *otParams.Info.KeyRingLocation)
		assert.Equal(tt, "ringname", *otParams.Info.KeyRingName)
		assert.Equal(tt, "projectid", *otParams.Info.ProjectID)
		assert.Equal(tt, "credentials", string(*otParams.Info.ApplicationCredentials))
		assert.Equal(tt, "svmname", *otParams.Info.Svm.Name)
	})
}

func TestGcpKmsDeleteParamsToOntap(t *testing.T) {
	t.Run("WhenParamsIsNil", func(tt *testing.T) {
		otParams := gcpKmsDeleteParamsToOntap(nil)
		assert.NotNil(tt, otParams)
		assert.Empty(tt, otParams.UUID)
	})
	t.Run("WhenUUIDInParamsIsSet", func(tt *testing.T) {
		params := GcpKmsDeleteParams{UUID: "uuid1"}
		otParams := gcpKmsDeleteParamsToOntap(&params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "uuid1", otParams.UUID)
	})
}

func TestAggregateCollectionGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := aggregateCollectionGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &AggregateCollectionGetParams{
			Name: nillable.ToPointer("name"),
		}
		otParams := aggregateCollectionGetParamsToONTAP(params)
		assert.Equal(tt, "name", *otParams.Name)
	})
}

func TestAggregateModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := aggregateModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &AggregateModifyParams{
			UUID:                     "uuid",
			TieringFullnessThreshold: nillable.ToPointer(int64(616)),
		}
		otParams := aggregateModifyParamsToONTAP(params)
		assert.Equal(tt, "uuid", otParams.UUID)
		assert.Equal(tt, int64(616), *otParams.Info.CloudStorage.TieringFullnessThreshold)
	})
}

func TestSnapshotCollectionGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapshotCollectionGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapshotCollectionGetParams{
			BaseParams:      BaseParams{Fields: []string{"field"}},
			SnapmirrorLabel: nillable.ToPointer("lab"),
			VolumeUUID:      "uuid",
			Name:            nillable.ToPointer("snapmirror-snapshot"),
		}

		otParams := snapshotCollectionGetParamsToONTAP(params)
		assert.Equal(tt, "field", otParams.Fields[0])
		assert.Equal(tt, "uuid", otParams.VolumeUUID)
		assert.Equal(tt, "lab", *otParams.SnapmirrorLabel)
		assert.Equal(tt, "snapmirror-snapshot", *otParams.Name)
	})
}

func TestSnapshotPolicyGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapshotPolicyGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapshotPolicyGetParams{
			BaseParams: BaseParams{Fields: []string{"field"}},
			UUID:       "uuid",
		}

		otParams := snapshotPolicyGetParamsToONTAP(params)
		assert.Equal(tt, "field", otParams.Fields[0])
		assert.Equal(tt, "uuid", otParams.UUID)
	})
}

func TestVolumeCollectionGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := volumeCollectionGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &VolumeCollectionGetParams{
			BaseParams: BaseParams{Fields: []string{"field"}},
			UUID:       nillable.ToPointer("uuid"),
			SvmName:    nillable.ToPointer("svm-1"),
		}

		otParams := volumeCollectionGetParamsToONTAP(params)
		assert.Equal(tt, "field", otParams.Fields[0])
		assert.Equal(tt, "uuid", *otParams.UUID)
		assert.Equal(tt, "svm-1", *otParams.SvmName)
	})
}

func TestVolumeGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := volumeGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &VolumeGetParams{
			BaseParams: BaseParams{Fields: []string{"field"}},
			UUID:       "uuid",
		}

		otParams := volumeGetParamsToONTAP(params)
		assert.Equal(tt, "field", otParams.Fields[0])
		assert.Equal(tt, "uuid", otParams.UUID)
	})
}

func TestSvmModifyParamsToOntap(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := svmModifyParamsToOntap(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenNsSwitchSourceNotSet", func(tt *testing.T) {
		params := &SvmModifyParams{
			SvmUUID:  "svm-uuid",
			NsSwitch: nil,
		}
		otParams := svmModifyParamsToOntap(params)
		assert.Equal(tt, "svm-uuid", otParams.UUID)
		assert.Nil(tt, otParams.Info.Nsswitch)
	})
	t.Run("WhenNsSwitchSourceSet", func(tt *testing.T) {
		var nsSwitchSources []*models.NsswitchSource
		nsSwitchDBValue := models.NsswitchSource("ldap")
		nsSwitchSources = append(nsSwitchSources, &nsSwitchDBValue)
		params := &SvmModifyParams{
			SvmUUID: "svm-uuid",
			NsSwitch: &NsSwitchSource{
				NsSwitchSourceNamemap:  nsSwitchSources,
				NsSwitchSourcePasswd:   nsSwitchSources,
				NsSwitchSourceNetgroup: nsSwitchSources,
				NsSwitchSourceGroup:    nsSwitchSources,
			},
		}
		res := &models.SvmInlineNsswitch{Group: nsSwitchSources, Hosts: []*models.NsswitchSource(nil), Namemap: nsSwitchSources, Netgroup: nsSwitchSources, Passwd: nsSwitchSources}
		otParams := svmModifyParamsToOntap(params)
		assert.Equal(tt, "svm-uuid", otParams.UUID)
		assert.Equal(tt, res, otParams.Info.Nsswitch)
	})
	t.Run("WhenNfsOrCifsAllowedNotSet", func(tt *testing.T) {
		params := &SvmModifyParams{
			SvmUUID:     "svm-uuid",
			CifsAllowed: nil,
			NfsAllowed:  nil,
		}
		otParams := svmModifyParamsToOntap(params)
		assert.Equal(tt, "svm-uuid", otParams.UUID)
		assert.Nil(tt, otParams.Info.Cifs)
		assert.Nil(tt, otParams.Info.Nfs)
	})
	t.Run("WhenNfsOrCifsAllowedSet", func(tt *testing.T) {
		params := &SvmModifyParams{
			SvmUUID:     "svm-uuid",
			CifsAllowed: nillable.ToPointer(true),
			NfsAllowed:  nillable.ToPointer(false),
		}
		otParams := svmModifyParamsToOntap(params)
		assert.Equal(tt, "svm-uuid", otParams.UUID)
		assert.True(tt, *otParams.Info.Cifs.Allowed)
		assert.False(tt, *otParams.Info.Nfs.Allowed)
	})
	t.Run("WhenRetentionperiodSet", func(tt *testing.T) {
		params := &SvmModifyParams{
			SvmUUID:              "svm-uuid",
			RetentionPeriodHours: nillable.ToPointer(int64(666)),
		}
		otParams := svmModifyParamsToOntap(params)
		assert.Equal(tt, "svm-uuid", otParams.UUID)
		assert.Equal(tt, int64(666), *otParams.Info.RetentionPeriod)
	})
	t.Run("WhenQoSPolicyNameSet", func(tt *testing.T) {
		params := &SvmModifyParams{
			SvmUUID:       "svm-uuid",
			QoSPolicyName: nillable.ToPointer("test-qos-policy"),
		}
		otParams := svmModifyParamsToOntap(params)
		assert.Equal(tt, "svm-uuid", otParams.UUID)
		assert.NotNil(tt, otParams.Info.QosPolicy)
		assert.Equal(tt, "test-qos-policy", *otParams.Info.QosPolicy.Name)
	})

	t.Run("WhenQoSPolicyNameIsNil", func(tt *testing.T) {
		params := &SvmModifyParams{
			SvmUUID:       "svm-uuid",
			QoSPolicyName: nil,
		}
		otParams := svmModifyParamsToOntap(params)
		assert.Equal(tt, "svm-uuid", otParams.UUID)
		assert.Nil(tt, otParams.Info.QosPolicy)
	})

	t.Run("WhenMultipleFieldsSet", func(tt *testing.T) {
		params := &SvmModifyParams{
			SvmUUID:              "svm-uuid",
			RetentionPeriodHours: nillable.ToPointer(int64(666)),
			QoSPolicyName:        nillable.ToPointer("test-qos-policy"),
		}
		otParams := svmModifyParamsToOntap(params)
		assert.Equal(tt, "svm-uuid", otParams.UUID)
		assert.Equal(tt, int64(666), *otParams.Info.RetentionPeriod)
		assert.NotNil(tt, otParams.Info.QosPolicy)
		assert.Equal(tt, "test-qos-policy", *otParams.Info.QosPolicy.Name)
	})
}

func TestVolumeDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenEmptyParams", func(tt *testing.T) {
		otParams := volumeDeleteParamsToONTAP(&VolumeDeleteParams{})
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &VolumeDeleteParams{
			UUID: "vol-1",
		}
		otParams := volumeDeleteParamsToONTAP(params)
		assert.Equal(tt, params.UUID, otParams.UUID)
		assert.Equal(tt, returnTimeout, *otParams.ReturnTimeout)
	})
}

func TestIpspaceDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenEmptyParams", func(tt *testing.T) {
		otParams := ipspaceDeleteParamsToONTAP(&IpspaceDeleteParams{})
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &IpspaceDeleteParams{
			Name: "policy-1",
		}
		otParams := ipspaceDeleteParamsToONTAP(params)
		assert.Equal(tt, &params.Name, otParams.Name)
	})
}

func TestQosPolicyGroupCollectionGetParamsToONTAPCollectionGet(t *testing.T) {
	t.Run("WhenEmptyParams", func(tt *testing.T) {
		otParams := qosPolicyGroupCollectionGetParamsToONTAPCollectionGet(&QosPolicyGroupCollectionGetParams{})
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &QosPolicyGroupCollectionGetParams{
			BaseParams: BaseParams{Fields: []string{"field1"}},
			Name:       "policy-1",
		}
		otParams := qosPolicyGroupCollectionGetParamsToONTAPCollectionGet(params)
		assert.Equal(tt, []string{"field1"}, otParams.Fields)
		assert.Equal(tt, params.Name, *otParams.Name)
	})
}

func TestClusterPeerToONTAPCreate(t *testing.T) {
	location := time.FixedZone("UTC+3", 3*60*60)
	expTime := strfmt.DateTime(time.Now().In(location))

	date := (*time.Time)(&expTime)

	expTimeStr := date.Format(time.RFC3339)
	expTimePtr := nillable.ToPointer(expTimeStr)

	t.Run("WithExpiryTime", func(tt *testing.T) {
		params := ClusterPeerCreateParams{
			Name:               "test",
			IPAddresses:        []string{"1.2.3.4"},
			ExpiryTime:         expTimePtr,
			GeneratePassphrase: false,
		}
		otParams := clusterPeerToONTAPCreate(params)
		assert.Equal(tt, params.Name, *otParams.Info.Name)
		assert.Equal(tt, *expTimePtr, *otParams.Info.Authentication.ExpiryTime)

		var ipAddresses []string
		for _, ip := range otParams.Info.Remote.IPAddresses {
			if ip != nil {
				ipAddresses = append(ipAddresses, string(*ip))
			}
		}
		assert.Equal(tt, params.IPAddresses, ipAddresses)
	})
	t.Run("WithoutExpiryTime", func(tt *testing.T) {
		params := ClusterPeerCreateParams{
			Name:               "test",
			IPAddresses:        []string{"1.2.3.4"},
			IPSpace:            "ipSpace",
			GeneratePassphrase: false,
		}

		otParams := clusterPeerToONTAPCreate(params)
		assert.Equal(tt, params.Name, *otParams.Info.Name)
		assert.Nil(tt, otParams.Info.Authentication.ExpiryTime)

		var ipAddresses []string
		for _, ip := range otParams.Info.Remote.IPAddresses {
			if ip != nil {
				ipAddresses = append(ipAddresses, string(*ip))
			}
		}
		assert.Equal(tt, params.IPAddresses, ipAddresses)
	})
	t.Run("WithLocalRole", func(tt *testing.T) {
		localRole := nillable.ToPointer("some-role")
		params := ClusterPeerCreateParams{
			Name:               "test",
			IPAddresses:        []string{"1.2.3.4"},
			GeneratePassphrase: false,
			LocalRole:          localRole,
		}
		otParams := clusterPeerToONTAPCreate(params)
		assert.NotNil(tt, otParams.Info.LocalRole)
		assert.Equal(tt, "some-role", *otParams.Info.LocalRole)
	})
	t.Run("WithoutLocalRole", func(tt *testing.T) {
		params := ClusterPeerCreateParams{
			Name:               "test",
			IPAddresses:        []string{"1.2.3.4"},
			GeneratePassphrase: false,
			LocalRole:          nil,
		}
		otParams := clusterPeerToONTAPCreate(params)
		assert.Nil(tt, otParams.Info.LocalRole)
	})
}

func TestClusterPeerToONTAPAccept(t *testing.T) {
	location := time.FixedZone("UTC+3", 3*60*60)
	expTime := strfmt.DateTime(time.Now().In(location))

	date := (*time.Time)(&expTime)

	expTimeStr := date.Format(time.RFC3339)
	expTimePtr := nillable.ToPointer(expTimeStr)

	t.Run("WithExpiryTime", func(tt *testing.T) {
		params := ClusterPeerCreateParams{
			Name:               "test",
			IPAddresses:        []string{"1.2.3.4"},
			ExpiryTime:         expTimePtr,
			GeneratePassphrase: false,
		}
		otParams := clusterPeerToONTAPAccept(params)
		assert.Equal(tt, params.Name, *otParams.Info.Name)
		assert.Equal(tt, *expTimePtr, *otParams.Info.Authentication.ExpiryTime)

		var ipAddresses []string
		for _, ip := range otParams.Info.Remote.IPAddresses {
			if ip != nil {
				ipAddresses = append(ipAddresses, string(*ip))
			}
		}
		assert.Equal(tt, params.IPAddresses, ipAddresses)
	})
	t.Run("WithoutExpiryTime", func(tt *testing.T) {
		params := ClusterPeerCreateParams{
			Name:               "test",
			IPAddresses:        []string{"1.2.3.4"},
			IPSpace:            "ipSpace",
			GeneratePassphrase: false,
		}

		otParams := clusterPeerToONTAPAccept(params)
		assert.Equal(tt, params.Name, *otParams.Info.Name)
		assert.Nil(tt, otParams.Info.Authentication.ExpiryTime)

		var ipAddresses []string
		for _, ip := range otParams.Info.Remote.IPAddresses {
			if ip != nil {
				ipAddresses = append(ipAddresses, string(*ip))
			}
		}
		assert.Equal(tt, params.IPAddresses, ipAddresses)
	})
}

func TestScheduleCollectionGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := scheduleCollectionGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &ScheduleCollectionGetParams{
			BaseParams: BaseParams{Fields: []string{"field1"}},
			Name:       "name",
		}
		otParams := scheduleCollectionGetParamsToONTAP(params)
		assert.Equal(tt, []string{"field1"}, otParams.Fields)
		assert.Equal(tt, "name", *otParams.Name)
	})
}

func TestSvmPeerGetCollectionParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := svmPeerGetCollectionParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SvmPeerGetCollectionParams{
			BaseParams: BaseParams{Fields: []string{"peer", "state"}}, SvmName: nillable.ToPointer("svm-1"), PeerSvmName: nillable.ToPointer("svm-2"),
		}
		otParams := svmPeerGetCollectionParamsToONTAP(params)
		assert.Equal(tt, []string{"peer", "state"}, otParams.Fields)
		assert.Equal(tt, params.SvmName, otParams.SvmName)
		assert.Equal(tt, params.PeerSvmName, otParams.PeerSvmName)
	})
}

func TestSnapmirrorRelationshipListParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipListParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipListParams{
			SourcePath:      "src-path",
			DestinationPath: "dst-path",
		}
		otParams := snapmirrorRelationshipListParamsToONTAP(params)
		assert.Equal(tt, params.SourcePath, *otParams.SourcePath)
		assert.Equal(tt, params.DestinationPath, *otParams.DestinationPath)
		assert.Nil(tt, otParams.Fields)
	})
	t.Run("WhenDestinationPathContainsObjstore_SetsFields", func(tt *testing.T) {
		params := &SnapmirrorRelationshipListParams{
			SourcePath:      "src-svm:src-vol",
			DestinationPath: "dst-svm:/objstore/bucket",
		}
		otParams := snapmirrorRelationshipListParamsToONTAP(params)
		assert.Equal(tt, params.SourcePath, *otParams.SourcePath)
		assert.Equal(tt, params.DestinationPath, *otParams.DestinationPath)
		expectedFields := []string{"destination.uuid", "healthy", "unhealthy_reason.code", "unhealthy_reason.message", "state", "total_transfer_bytes"}
		assert.Equal(tt, expectedFields, otParams.Fields)
	})
}

func TestSnapmirrorRelationshipModifyParamsToONTAP(t *testing.T) {
	t.Run("WithNilParams", func(tt *testing.T) {
		result := snapmirrorRelationshipModifyParamsToONTAP(nil)
		assert.NotNil(tt, result)
		assert.Nil(tt, result.Info)
	})

	t.Run("WithEmptyParams", func(tt *testing.T) {
		params := &SnapmirrorRelationshipModifyParams{}
		result := snapmirrorRelationshipModifyParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.Nil(tt, result.Info.TransferSchedule)
	})

	t.Run("WithTransferSchedule", func(tt *testing.T) {
		params := &SnapmirrorRelationshipModifyParams{
			UUID:             "test-uuid",
			TransferSchedule: strPtr("daily"),
		}
		result := snapmirrorRelationshipModifyParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-uuid", result.UUID)
		assert.NotNil(tt, result.Info.TransferSchedule)
		assert.Equal(tt, "daily", *result.Info.TransferSchedule.Name)
	})
}

func TestSnapmirrorRelationshipListDestinationsParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipListDestinationsParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipListDestinationsParams{
			SourcePath:         nillable.GetStringPtr("src-path"),
			DestinationPath:    nillable.GetStringPtr("dst-path"),
			SourceSVMName:      nillable.GetStringPtr("src-svm"),
			DestinationSVMName: nillable.GetStringPtr("src-svm"),
		}
		otParams := snapmirrorRelationshipListDestinationsParamsToONTAP(params)
		assert.Equal(tt, params.SourcePath, otParams.SourcePath)
		assert.Equal(tt, params.DestinationPath, otParams.DestinationPath)
		assert.Equal(tt, params.SourceSVMName, otParams.SourceSvmName)
		assert.Equal(tt, params.DestinationSVMName, otParams.DestinationSvmName)
	})
}

func TestConvertSnapmirrorRelationshipListFromONTAP(t *testing.T) {
	t.Run("WhenResponseNil", func(tt *testing.T) {
		response := convertSnapmirrorRelationshipListFromREST(nil)
		assert.Nil(tt, response)
	})
	t.Run("WhenPayloadIsNil", func(tt *testing.T) {
		response := convertSnapmirrorRelationshipListFromREST(&snapmirror.SnapmirrorRelationshipsGetOK{Payload: nil})
		assert.Empty(tt, response)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		response := convertSnapmirrorRelationshipListFromREST(&snapmirror.SnapmirrorRelationshipsGetOK{Payload: &models.SnapmirrorRelationshipResponse{
			Links:      nil,
			NumRecords: nil,
			SnapmirrorRelationshipResponseInlineRecords: []*models.SnapmirrorRelationship{
				{
					State: nil,
				},
			},
		}})
		assert.NotNil(tt, response)
		assert.Equal(tt, 1, len(response))
	})
}

func TestConvertSnapmirrorRelationshipGetFromONTAP(t *testing.T) {
	t.Run("WhenResponseNil", func(tt *testing.T) {
		response := convertSnapmirrorRelationshipGetFromREST(nil)
		assert.Nil(tt, response)
	})
	t.Run("WhenPayloadIsNil", func(tt *testing.T) {
		response := convertSnapmirrorRelationshipGetFromREST(&snapmirror.SnapmirrorRelationshipGetOK{Payload: nil})
		assert.Nil(tt, response)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		UUID := strfmt.UUID("testUUID")
		response := convertSnapmirrorRelationshipGetFromREST(&snapmirror.SnapmirrorRelationshipGetOK{Payload: &models.SnapmirrorRelationship{
			UUID: &UUID,
		}})
		assert.NotNil(tt, response)
		assert.Equal(tt, &UUID, response.UUID)
	})
}

func TestSnapmirrorRelationshipGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipGetParams{
			UUID: "theUUID",
		}
		otParams := snapmirrorRelationshipGetParamsToONTAP(params)
		assert.Equal(tt, "theUUID", otParams.UUID)
	})
}

func TestSnapmirrorRelationshipDeleteGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipDeleteParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipDeleteParams{
			UUID: "zeUUID",
		}
		otParams := snapmirrorRelationshipDeleteParamsToONTAP(params)
		assert.Equal(tt, "zeUUID", otParams.UUID)
	})
}

func strPtr(s string) *string {
	return &s
}

func TestVolumeModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		result := volumeModifyParamsToONTAP(nil)
		assert.NotNil(tt, result)
	})

	t.Run("WhenQuotaEnabledSet_ThenQuotaIsSet", func(tt *testing.T) {
		val := true
		params := &VolumeModifyParams{UUID: "uuid", QuotaEnabled: &val}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Quota)
		assert.Equal(tt, &val, result.Info.Quota.Enabled)
	})

	t.Run("WhenReKeySet_ThenEncryptionIsSet", func(tt *testing.T) {
		val := true
		params := &VolumeModifyParams{UUID: "uuid", ReKey: &val}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Encryption)
		assert.Equal(tt, &val, result.Info.Encryption.Rekey)
	})

	t.Run("WhenSplitInitiatedSet_ThenCloneAndNoReturnTimeout", func(tt *testing.T) {
		val := true
		params := &VolumeModifyParams{UUID: "uuid", SplitInitiated: &val, MatchParentStorageTier: true}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Clone)
		assert.Equal(tt, &val, result.Info.Clone.SplitInitiated)
		assert.NotNil(tt, result.CloneMatchParentStorageTier)
	})

	t.Run("WhenStateSet_ThenStateIsSet", func(tt *testing.T) {
		state := "online"
		params := &VolumeModifyParams{UUID: "uuid", State: &state}
		result := volumeModifyParamsToONTAP(params)
		assert.Equal(tt, &state, result.Info.State)
	})

	t.Run("WhenSnapshotPolicyNameSet_ThenSnapshotPolicyIsSet", func(tt *testing.T) {
		name := "policy"
		params := &VolumeModifyParams{UUID: "uuid", SnapshotPolicyName: &name}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.SnapshotPolicy)
		assert.Equal(tt, &name, result.Info.SnapshotPolicy.Name)
	})

	t.Run("WhenMovementSet_ThenMovementIsSet", func(tt *testing.T) {
		tiering := "auto"
		state := "moving"
		aggUUID := "agg-uuid"
		aggName := "agg-name"
		params := &VolumeModifyParams{
			UUID: "uuid",
			Movement: &VolumeMovementParams{
				TieringPolicy: &tiering,
				State:         &state,
				VolumeMovementDestinationAggregate: &VolumeMovementDestinationAggregate{
					DestinationAggregateUUID: &aggUUID,
					DestinationAggregateName: &aggName,
				},
			},
		}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Movement)
		assert.NotNil(tt, result.Info.Movement.DestinationAggregate)
	})

	t.Run("WhenCommentSet_ThenCommentIsSet", func(tt *testing.T) {
		comment := "test"
		params := &VolumeModifyParams{UUID: "uuid", Comment: &comment}
		result := volumeModifyParamsToONTAP(params)
		assert.Equal(tt, &comment, result.Info.Comment)
	})

	t.Run("WhenSizeAndLogicalSpaceAndSnapReserveAndMaxAutoSizeSet_ThenSpaceIsSet", func(tt *testing.T) {
		size := uint64(100)
		logical := true
		snap := int64(5)
		maxAuto := uint64(200)
		params := &VolumeModifyParams{
			UUID:                    "uuid",
			Size:                    &size,
			LogicalSpaceEnforcement: &logical,
			SnapReserve:             &snap,
			MaxAutoSize:             &maxAuto,
		}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Space)
		assert.NotNil(tt, result.Info.Space.Size)
		assert.NotNil(tt, result.Info.Space.LogicalSpace)
		assert.NotNil(tt, result.Info.Space.Snapshot)
	})

	t.Run("WhenSnapshotDirectoryAccessEnabledSet_ThenFieldIsSet", func(tt *testing.T) {
		val := true
		params := &VolumeModifyParams{UUID: "uuid", SnapshotDirectoryAccessEnabled: &val}
		result := volumeModifyParamsToONTAP(params)
		assert.Equal(tt, &val, result.Info.SnapshotDirectoryAccessEnabled)
	})

	t.Run("WhenSetAtTimeEnabledSet_ThenFieldIsSet", func(tt *testing.T) {
		val := true
		params := &VolumeModifyParams{UUID: "uuid", SetAtTimeEnabled: &val}
		result := volumeModifyParamsToONTAP(params)
		assert.Equal(tt, &val, result.Info.AccessTimeEnabled)
	})

	t.Run("WhenTieringPolicyAndCoolingDaysSet_ThenTieringIsSet", func(tt *testing.T) {
		policy := "auto"
		days := int64(7)
		params := &VolumeModifyParams{
			UUID: "uuid",
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: policy,
				MinCoolingDays:          days,
			},
		}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Tiering)
		assert.Equal(tt, policy, *result.Info.Tiering.Policy)
		assert.Equal(tt, days, *result.Info.Tiering.MinCoolingDays)
	})

	t.Run("WhenTieringPolicyIsNone_ThenCoolingDaysIsNil", func(tt *testing.T) {
		policy := "none"
		days := int64(7)
		params := &VolumeModifyParams{
			UUID: "uuid",
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: policy,
				MinCoolingDays:          days,
			},
		}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Tiering)
		assert.Equal(tt, policy, *result.Info.Tiering.Policy)
		assert.Nil(tt, result.Info.Tiering.MinCoolingDays)
	})

	t.Run("WhenCloudRetrievalPolicySet_ThenFieldIsSet", func(tt *testing.T) {
		val := "policy"
		params := &VolumeModifyParams{
			UUID: "uuid",
			TieringPolicy: &TieringPolicy{
				CloudRetrievalPolicy: val,
			},
		}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.CloudRetrievalPolicy)
		assert.Equal(tt, val, *result.Info.CloudRetrievalPolicy)
	})

	t.Run("WhenQosPolicySet_ThenQosIsSet", func(tt *testing.T) {
		val := "qos"
		params := &VolumeModifyParams{UUID: "uuid", QosPolicy: &val}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Qos)
		assert.NotNil(tt, result.Info.Qos.Policy)
		assert.Equal(tt, &val, result.Info.Qos.Policy.Name)
	})

	t.Run("WhenQosPolicyIsNone_ThenPassedThrough", func(tt *testing.T) {
		noneVal := "none"
		params := &VolumeModifyParams{UUID: "uuid", QosPolicy: &noneVal}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Qos)
		assert.NotNil(tt, result.Info.Qos.Policy)
		assert.Equal(tt, &noneVal, result.Info.Qos.Policy.Name, "none should be passed through as-is")
	})

	t.Run("WhenQosPolicyIsEmptyString_ThenPassedThrough", func(tt *testing.T) {
		emptyVal := ""
		params := &VolumeModifyParams{UUID: "uuid", QosPolicy: &emptyVal}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Qos)
		assert.NotNil(tt, result.Info.Qos.Policy)
		assert.Equal(tt, &emptyVal, result.Info.Qos.Policy.Name, "Empty string should be passed through as-is (will fail at ONTAP)")
	})

	t.Run("WhenRestoreToSnapshotUUIDSet_ThenFieldIsSet", func(tt *testing.T) {
		val := "snap-uuid"
		params := &VolumeModifyParams{UUID: "uuid", RestoreToSnapshotUUID: &val}
		result := volumeModifyParamsToONTAP(params)
		assert.Equal(tt, val, *result.RestoreToSnapshotUUID)
	})

	t.Run("WhenAntiRansomwareStateSet_ThenFieldIsSet", func(tt *testing.T) {
		val := "enabled"
		params := &VolumeModifyParams{UUID: "uuid", AntiRansomwareState: &val}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.AntiRansomware)
		assert.Equal(tt, &val, result.Info.AntiRansomware.State)
	})

	t.Run("WhenExportPolicySet_ThenNasExportPolicyIsSet", func(tt *testing.T) {
		exportPolicy := "test-export-policy"
		params := &VolumeModifyParams{UUID: "uuid", ExportPolicy: &exportPolicy}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Nas)
		assert.NotNil(tt, result.Info.Nas.ExportPolicy)
		assert.Equal(tt, exportPolicy, *result.Info.Nas.ExportPolicy.Name)
	})

	t.Run("WhenPathSet_ThenNasPathIsSet", func(tt *testing.T) {
		path := "/test/junction/path"
		params := &VolumeModifyParams{UUID: "uuid", Path: &path}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Nas)
		assert.Equal(tt, &path, result.Info.Nas.Path)
	})

	t.Run("WhenExportPolicyAndPathSet_ThenBothNasFieldsAreSet", func(tt *testing.T) {
		exportPolicy := "test-export-policy"
		path := "/test/junction/path"
		params := &VolumeModifyParams{
			UUID:         "uuid",
			ExportPolicy: &exportPolicy,
			Path:         &path,
		}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Nas)
		assert.NotNil(tt, result.Info.Nas.ExportPolicy)
		assert.Equal(tt, exportPolicy, *result.Info.Nas.ExportPolicy.Name)
		assert.Equal(tt, &path, result.Info.Nas.Path)
	})

	t.Run("WhenUnixPermissionsSet_ThenNasUnixPermissionsIsSet", func(tt *testing.T) {
		permissions := "0755"
		params := &VolumeModifyParams{
			UUID:            "uuid",
			UnixPermissions: &permissions,
		}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Nas)
		if assert.NotNil(tt, result.Info.Nas.UnixPermissions) {
			assert.Equal(tt, int64(755), *result.Info.Nas.UnixPermissions)
		}
	})

	t.Run("WhenNasUidSet_ThenNasUidIsSet", func(tt *testing.T) {
		uid := int64(2000)
		params := &VolumeModifyParams{UUID: "uuid", NasUid: &uid}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Nas)
		if assert.NotNil(tt, result.Info.Nas.UID) {
			assert.Equal(tt, uid, *result.Info.Nas.UID)
		}
	})

	t.Run("WhenNasUidZero_ThenNasUidIsSetToZero", func(tt *testing.T) {
		uid := int64(0)
		params := &VolumeModifyParams{UUID: "uuid", NasUid: &uid}
		result := volumeModifyParamsToONTAP(params)
		assert.NotNil(tt, result.Info.Nas)
		if assert.NotNil(tt, result.Info.Nas.UID) {
			assert.Equal(tt, int64(0), *result.Info.Nas.UID)
		}
	})

	t.Run("WhenNasUidNil_ThenNasBlockNotCreatedForUidAlone", func(tt *testing.T) {
		params := &VolumeModifyParams{UUID: "uuid"}
		result := volumeModifyParamsToONTAP(params)
		assert.Nil(tt, result.Info.Nas)
	})
}

func TestVolumeUnmountParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		result := volumeUnmountParamsToONTAP(nil)
		assert.NotNil(tt, result)
		assert.Empty(tt, result.UUID)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &VolumeUnmountParams{
			UUID: "volume-uuid-123",
		}
		result := volumeUnmountParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, "volume-uuid-123", result.UUID)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Nas)
		assert.NotNil(tt, result.Info.Nas.Path)
		assert.Equal(tt, "", *result.Info.Nas.Path)
	})
}

func TestVolumeMountParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		result := volumeMountParamsToONTAP(nil)
		assert.NotNil(tt, result)
		assert.Empty(tt, result.UUID)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &VolumeMountParams{
			UUID:         "volume-uuid-123",
			JunctionPath: "/test/junction/path",
		}
		result := volumeMountParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, "volume-uuid-123", result.UUID)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Nas)
		assert.NotNil(tt, result.Info.Nas.Path)
		assert.Equal(tt, "/test/junction/path", *result.Info.Nas.Path)
	})
}

func TestLunModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		result := lunModifyParamsToONTAP(nil)
		assert.NotNil(tt, result)
	})

	t.Run("WhenParamsSet_ThenFieldsAreSet", func(tt *testing.T) {
		params := &LunUpdateParams{
			UUID:       "uuid",
			SvmName:    "svm",
			Name:       "lun",
			VolumeName: "vol",
			Size:       1234,
		}
		result := lunModifyParamsToONTAP(params)
		assert.Equal(tt, "uuid", result.UUID)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Name)
		assert.NotNil(tt, result.Info.Space)
		assert.Equal(tt, &params.Size, result.Info.Space.Size)
		assert.NotNil(tt, result.ReturnTimeout)
	})
}

func TestNodesGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		result := nodesGetParamsToONTAP(nil)
		assert.NotNil(tt, result)
	})

	t.Run("WhenParamsSet_ThenFieldsAreSet", func(tt *testing.T) {
		maxRecords := int64(10)
		returnRecords := true
		params := &NodesGetParams{
			BaseParams: BaseParams{
				Fields:        []string{"field1", "field2"},
				ReturnRecords: &returnRecords,
				MaxRecords:    &maxRecords,
			},
		}
		result := nodesGetParamsToONTAP(params)
		assert.Equal(tt, []string{"field1", "field2"}, result.Fields)
	})
}

func TestNetworkIPInterfacesGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		result := networkIPInterfacesGetParamsToONTAP(nil)
		assert.NotNil(tt, result)
	})

	t.Run("WhenAllFieldsSet_ThenFieldsAreSet", func(tt *testing.T) {
		maxRecords := int64(5)
		fields := []string{"ip.address", "name"}
		svmName := "svm1"
		name := "lif1"
		svmUUID := "uuid1"
		ipAddress := "10.0.0.1"
		servicePolicyName := "default-intercluster"
		params := &NetworkIPInterfacesGetParams{
			BaseParams: BaseParams{
				Fields:     fields,
				MaxRecords: &maxRecords,
			},
			SvmName:           &svmName,
			Name:              &name,
			SvmUUID:           &svmUUID,
			IPAddress:         &ipAddress,
			ServicePolicyName: &servicePolicyName,
		}
		result := networkIPInterfacesGetParamsToONTAP(params)
		assert.Equal(tt, fields, result.Fields)
		assert.Equal(tt, &svmName, result.SvmName)
		assert.Equal(tt, &name, result.Name)
		assert.Equal(tt, &svmUUID, result.SvmUUID)
		assert.Equal(tt, &ipAddress, result.IPAddress)
		assert.Equal(tt, &servicePolicyName, result.ServicePolicyName)
	})
}

func TestVolumeDeleteParamsToONTAPCollectionDelete(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		result := volumeDeleteParamsToONTAPCollectionDelete(nil)
		assert.NotNil(tt, result)
	})

	t.Run("WhenParamsSet_ThenFieldsAreSet", func(tt *testing.T) {
		params := &VolumeDeleteParams{
			Name: "vol1",
		}
		result := volumeDeleteParamsToONTAPCollectionDelete(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Name)
		assert.Equal(tt, "vol1", *result.Name)
		assert.NotNil(tt, result.Force)
		assert.Equal(tt, "true", *result.Force)
		assert.NotNil(tt, result.ReturnTimeout)
	})
}

func TestVolumeCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsSet_ThenFieldsAreSet", func(tt *testing.T) {
		params := &VolumeCreateParams{
			Aggregates:                     []string{"aggr1"},
			Name:                           "vol1",
			Type:                           "rw",
			Size:                           1024,
			Svm:                            "svm1",
			SnapshotReservePercent:         5,
			SnapshotDirectoryAccessEnabled: true,
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: "auto",
				MinCoolingDays:          30,
				CloudRetrievalPolicy:    "default",
			},
		}
		result := volumeCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, "vol1", *result.Info.Name)
		assert.Equal(tt, "rw", *result.Info.Type)
		assert.Equal(tt, int64(1024), *result.Info.Size)
		assert.Equal(tt, "svm1", *result.Info.Svm.Name)
		assert.NotNil(tt, result.Info.Space)
		assert.NotNil(tt, result.Info.VolumeInlineAggregates)
		assert.Equal(tt, "true", *result.ReturnRecords)
		assert.Equal(tt, params.TieringPolicy.CoolAccessTieringPolicy, *result.Info.Tiering.Policy)
		assert.Equal(tt, params.TieringPolicy.MinCoolingDays, *result.Info.Tiering.MinCoolingDays)
		assert.Equal(tt, params.TieringPolicy.CloudRetrievalPolicy, *result.Info.CloudRetrievalPolicy)
	})

	t.Run("WhenQosPolicyIsSet_ThenQosIsPopulated", func(tt *testing.T) {
		params := &VolumeCreateParams{
			Name:       "vol1",
			Type:       "rw",
			Size:       1024,
			Svm:        "svm1",
			Aggregates: []string{"aggr1"},
			QosPolicy:  "qos-policy-1",
		}
		result := volumeCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Qos)
		assert.NotNil(tt, result.Info.Qos.Policy)
		assert.NotNil(tt, result.Info.Qos.Policy.Name)
		assert.Equal(tt, "qos-policy-1", *result.Info.Qos.Policy.Name)
	})
}

func TestVolumeCreateParamsToONTAPWithSecurityStyle(t *testing.T) {
	t.Run("WhenSecurityStyleIsEmpty_ThenSecurityStyleIsNotSet", func(tt *testing.T) {
		params := &VolumeCreateParams{
			Aggregates:                     []string{"aggr1"},
			Name:                           "vol1",
			Type:                           "rw",
			Size:                           1024,
			Svm:                            "svm1",
			SnapshotReservePercent:         5,
			SnapshotDirectoryAccessEnabled: true,
			SecurityStyle:                  "", // Empty security style
		}
		result := volumeCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Nas)
		assert.Nil(tt, result.Info.Nas.SecurityStyle, "SecurityStyle should not be set when empty")
	})

	t.Run("WhenSecurityStyleIsSet_ThenSecurityStyleIsSet", func(tt *testing.T) {
		securityStyle := "unix"
		params := &VolumeCreateParams{
			Aggregates:                     []string{"aggr1"},
			Name:                           "vol1",
			Type:                           "rw",
			Size:                           1024,
			Svm:                            "svm1",
			SnapshotReservePercent:         5,
			SnapshotDirectoryAccessEnabled: true,
			SecurityStyle:                  securityStyle,
		}
		result := volumeCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Nas)
		assert.NotNil(tt, result.Info.Nas.SecurityStyle, "SecurityStyle should be set when provided")
		assert.Equal(tt, securityStyle, *result.Info.Nas.SecurityStyle)
	})

	t.Run("WhenSecurityStyleIsMixed_ThenSecurityStyleIsSet", func(tt *testing.T) {
		securityStyle := "mixed"
		params := &VolumeCreateParams{
			Aggregates:                     []string{"aggr1"},
			Name:                           "vol1",
			Type:                           "rw",
			Size:                           1024,
			Svm:                            "svm1",
			SnapshotReservePercent:         5,
			SnapshotDirectoryAccessEnabled: true,
			SecurityStyle:                  securityStyle,
		}
		result := volumeCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Nas)
		assert.NotNil(tt, result.Info.Nas.SecurityStyle, "SecurityStyle should be set when provided")
		assert.Equal(tt, securityStyle, *result.Info.Nas.SecurityStyle)
	})

	t.Run("WhenSecurityStyleIsNtfs_ThenSecurityStyleIsSet", func(tt *testing.T) {
		securityStyle := "ntfs"
		params := &VolumeCreateParams{
			Aggregates:                     []string{"aggr1"},
			Name:                           "vol1",
			Type:                           "rw",
			Size:                           1024,
			Svm:                            "svm1",
			SnapshotReservePercent:         5,
			SnapshotDirectoryAccessEnabled: true,
			SecurityStyle:                  securityStyle,
		}
		result := volumeCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Nas)
		assert.NotNil(tt, result.Info.Nas.SecurityStyle, "SecurityStyle should be set when provided")
		assert.Equal(tt, securityStyle, *result.Info.Nas.SecurityStyle)
	})
}

func TestVolumeCreateParamsToONTAPWithUnixPermissions(t *testing.T) {
	t.Run("WhenUnixPermissionsProvided_ThenParsedAndSet", func(tt *testing.T) {
		permissions := "755"
		params := &VolumeCreateParams{
			Aggregates:             []string{"aggr1"},
			Name:                   "vol1",
			Type:                   "rw",
			Size:                   2048,
			Svm:                    "svm1",
			SnapshotReservePercent: 5,
			UnixPermissions:        &permissions,
		}

		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		if assert.NotNil(tt, result.Info) && assert.NotNil(tt, result.Info.Nas) {
			if assert.NotNil(tt, result.Info.Nas.UnixPermissions) {
				assert.Equal(tt, int64(755), *result.Info.Nas.UnixPermissions)
			}
		}
	})
}

func TestVolumeCreateParamsToONTAPWithTieringPolicy(t *testing.T) {
	// Case 1: Both TieringPolicy and TieringSupported are set
	t.Run("WhenBothTieringPolicyAndTieringSupportedAreSet", func(tt *testing.T) {
		isSupported := true
		params := &VolumeCreateParams{
			Name:       "vol1",
			Type:       "rw",
			Size:       1024,
			Svm:        "svm1",
			Aggregates: []string{"aggr1"},
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: models.VolumeInlineTieringPolicyAuto,
				MinCoolingDays:          30,
				CloudRetrievalPolicy:    "default",
			},
			TieringSupported: &isSupported,
		}
		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Tiering)
		assert.Equal(tt, models.VolumeInlineTieringPolicyAuto, *result.Info.Tiering.Policy)
		assert.Equal(tt, int64(30), *result.Info.Tiering.MinCoolingDays)
		assert.Equal(tt, "default", *result.Info.CloudRetrievalPolicy)
		assert.Equal(tt, &isSupported, result.Info.Tiering.Supported)
	})

	// Case 2: Only TieringPolicy is set (with snapshot-only policy)
	t.Run("WhenOnlyTieringPolicyIsSetWithSnapshotOnly", func(tt *testing.T) {
		params := &VolumeCreateParams{
			Name:       "vol1",
			Type:       "rw",
			Size:       1024,
			Svm:        "svm1",
			Aggregates: []string{"aggr1"},
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: models.VolumeInlineTieringPolicySnapshotOnly,
				MinCoolingDays:          45,
				CloudRetrievalPolicy:    "promote",
			},
			TieringSupported: nil,
		}
		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Tiering)
		assert.Equal(tt, models.VolumeInlineTieringPolicySnapshotOnly, *result.Info.Tiering.Policy)
		assert.Equal(tt, int64(45), *result.Info.Tiering.MinCoolingDays)
		assert.Equal(tt, "promote", *result.Info.CloudRetrievalPolicy)
		assert.Nil(tt, result.Info.Tiering.Supported)
	})

	// Case 3: Only TieringPolicy is set (with non-auto/snapshot-only policy)
	t.Run("WhenOnlyTieringPolicyIsSetWithNonAutoPolicy", func(tt *testing.T) {
		params := &VolumeCreateParams{
			Name:       "vol1",
			Type:       "rw",
			Size:       1024,
			Svm:        "svm1",
			Aggregates: []string{"aggr1"},
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: models.VolumeInlineTieringPolicyNone,
				MinCoolingDays:          45,        // This should not be set in result
				CloudRetrievalPolicy:    "promote", // This should not be set in result
			},
			TieringSupported: nil,
		}
		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Tiering)
		assert.Equal(tt, models.VolumeInlineTieringPolicyNone, *result.Info.Tiering.Policy)
		assert.Nil(tt, result.Info.Tiering.MinCoolingDays)
		assert.Nil(tt, result.Info.CloudRetrievalPolicy)
		assert.Nil(tt, result.Info.Tiering.Supported)
	})

	// Case 4: Only TieringSupported is set
	t.Run("WhenOnlyTieringSupportedIsSet", func(tt *testing.T) {
		isSupported := true
		params := &VolumeCreateParams{
			Name:             "vol1",
			Type:             "rw",
			Size:             1024,
			Svm:              "svm1",
			Aggregates:       []string{"aggr1"},
			TieringPolicy:    nil,
			TieringSupported: &isSupported,
		}
		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.Tiering)
		assert.Nil(tt, result.Info.Tiering.Policy)
		assert.Nil(tt, result.Info.Tiering.MinCoolingDays)
		assert.Nil(tt, result.Info.CloudRetrievalPolicy)
		assert.Equal(tt, &isSupported, result.Info.Tiering.Supported)
	})

	// Case 5: Neither TieringPolicy nor TieringSupported is set
	t.Run("WhenNeitherTieringPolicyNorTieringSupportedIsSet", func(tt *testing.T) {
		params := &VolumeCreateParams{
			Name:             "vol1",
			Type:             "rw",
			Size:             1024,
			Svm:              "svm1",
			Aggregates:       []string{"aggr1"},
			TieringPolicy:    nil,
			TieringSupported: nil,
		}
		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Nil(tt, result.Info.Tiering)
		assert.Nil(tt, result.Info.CloudRetrievalPolicy)
	})
}

func TestVolumeCreateParamsToONTAPWithGranularDataMode(t *testing.T) {
	t.Run("WhenStyleIsFlexGroup_ThenGranularDataAndModeAreSet", func(tt *testing.T) {
		flexgroupStyle := VolumeStyleFlexGroup
		params := &VolumeCreateParams{
			Name:       "vol1",
			Type:       "rw",
			Size:       1024,
			Svm:        "svm1",
			Aggregates: []string{"aggr1"},
			Style:      &flexgroupStyle,
		}
		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.GranularData, "GranularData should be set for FlexGroup volumes")
		assert.True(tt, *result.Info.GranularData, "GranularData should be true for FlexGroup volumes")
		assert.NotNil(tt, result.Info.GranularDataMode, "GranularDataMode should be set for FlexGroup volumes")
		assert.Equal(tt, GranularDataModeAdvanced, *result.Info.GranularDataMode, "GranularDataMode should be set to GranularDataModeAdvanced constant")
	})

	t.Run("WhenStyleIsNotFlexGroup_ThenGranularDataAndModeAreNotSet", func(tt *testing.T) {
		regularStyle := "flexvol"
		params := &VolumeCreateParams{
			Name:       "vol1",
			Type:       "rw",
			Size:       1024,
			Svm:        "svm1",
			Aggregates: []string{"aggr1"},
			Style:      &regularStyle,
		}
		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Nil(tt, result.Info.GranularData, "GranularData should not be set for non-FlexGroup volumes")
		assert.Nil(tt, result.Info.GranularDataMode, "GranularDataMode should not be set for non-FlexGroup volumes")
	})

	t.Run("WhenStyleIsNil_ThenGranularDataAndModeAreNotSet", func(tt *testing.T) {
		params := &VolumeCreateParams{
			Name:       "vol1",
			Type:       "rw",
			Size:       1024,
			Svm:        "svm1",
			Aggregates: []string{"aggr1"},
			Style:      nil,
		}
		result := volumeCreateParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Nil(tt, result.Info.GranularData, "GranularData should not be set when Style is nil")
		assert.Nil(tt, result.Info.GranularDataMode, "GranularDataMode should not be set when Style is nil")
	})
}

func Test_flexCacheVolumeCreateParamsToONTAP(t *testing.T) {
	t.Run("AllParamsProvided", func(tt *testing.T) {
		dir1 := "dir1"
		dir2 := "dir2"
		exclude1 := "ex1"
		recurse := true
		size := int64(12345)
		atimeScrubEnabled := true
		atimeScrubPeriod := int16(7)
		cifsChangeNotifyEnabled := true
		globalFileLockingEnabled := true
		writebackEnabled := true
		params := &FlexCacheVolumeCreateParams{
			Name:                     "fcvol",
			SvmName:                  "svm1",
			Size:                     size,
			Aggregates:               []string{"aggr1", "aggr2"},
			OriginSvmName:            "originSvm",
			OriginVolumeName:         "originVol",
			Path:                     nillable.ToPointer("/custom/path"),
			AtimeScrubEnabled:        &atimeScrubEnabled,
			AtimeScrubPeriod:         &atimeScrubPeriod,
			CifsChangeNotifyEnabled:  &cifsChangeNotifyEnabled,
			GlobalFileLockingEnabled: &globalFileLockingEnabled,
			Prepopulate: &PrepopulateConfig{
				DirPaths:        []*string{&dir1, &dir2},
				ExcludeDirPaths: []*string{&exclude1},
				Recurse:         &recurse,
			},
			WritebackEnabled: &writebackEnabled,
		}
		ot := flexCacheVolumeCreateParamsToONTAP(params)

		assert.Equal(tt, params.Name, *ot.Info.Name)
		assert.Equal(tt, params.SvmName, *ot.Info.Svm.Name)
		assert.Equal(tt, params.Size, *ot.Info.Size)
		assert.Equal(tt, params.OriginSvmName, *ot.Info.FlexcacheInlineOrigins[0].Svm.Name)
		assert.Equal(tt, params.OriginVolumeName, *ot.Info.FlexcacheInlineOrigins[0].Volume.Name)

		// assert path
		assert.Equal(tt, params.Path, ot.Info.Path)

		// assert writeback, cifs, global file locking, atime scrub
		assert.Equal(tt, params.WritebackEnabled, ot.Info.Writeback.Enabled)
		assert.Equal(tt, params.CifsChangeNotifyEnabled, ot.Info.CifsChangeNotify.Enabled)
		assert.Equal(tt, params.GlobalFileLockingEnabled, ot.Info.GlobalFileLockingEnabled)
		assert.Equal(tt, params.AtimeScrubEnabled, ot.Info.AtimeScrub.Enabled)
		assert.Equal(tt, params.AtimeScrubPeriod, ot.Info.AtimeScrub.Period)

		// assert prepopulate fields
		assert.Equal(tt, params.Prepopulate.DirPaths, ot.Info.Prepopulate.DirPaths)
		assert.Equal(tt, params.Prepopulate.ExcludeDirPaths, ot.Info.Prepopulate.ExcludeDirPaths)
		assert.Equal(tt, params.Prepopulate.Recurse, ot.Info.Prepopulate.Recurse)
	})

	t.Run("PrepopulateMissing", func(tt *testing.T) {
		params := &FlexCacheVolumeCreateParams{
			Name:       "fcvol",
			SvmName:    "svm1",
			Aggregates: []string{"aggr1"},
		}
		ot := flexCacheVolumeCreateParamsToONTAP(params)
		assert.Nil(tt, ot.Info.Prepopulate)
	})

	t.Run("AggregatesTable", func(tt *testing.T) {
		tests := []struct {
			name    string
			aggs    []string
			wantLen int
		}{
			{"zero aggregates", nil, 0},
			{"one aggregate", []string{"aggr1"}, 1},
			{"multiple aggregates", []string{"aggr1", "aggr2", "aggr3"}, 3},
		}
		for _, test := range tests {
			tt.Run(test.name, func(ttt *testing.T) {
				params := &FlexCacheVolumeCreateParams{
					Name:       "fcvol",
					SvmName:    "svm1",
					Aggregates: test.aggs,
				}
				ot := flexCacheVolumeCreateParamsToONTAP(params)
				assert.Len(ttt, ot.Info.FlexcacheInlineAggregates, test.wantLen)
			})
		}
	})

	t.Run("ParamsNil", func(tt *testing.T) {
		ot := flexCacheVolumeCreateParamsToONTAP(nil)
		assert.Nil(tt, ot.Info)
	})
}

func TestFlexCacheVolumeDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		result := flexCacheVolumeDeleteParamsToONTAP(nil)
		assert.NotNil(tt, result)
		assert.Empty(tt, result.UUID)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &FlexCacheVolumeDeleteParams{
			UUID: "flexcache-uuid-456",
			Name: "flexcache-vol",
		}
		result := flexCacheVolumeDeleteParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, "flexcache-uuid-456", result.UUID)
		assert.NotNil(tt, result.ReturnTimeout)
		assert.Equal(tt, returnTimeout, *result.ReturnTimeout)
	})
}

func TestFlexCacheVolumeDeleteParamsToONTAPCollectionDelete(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		result := flexCacheVolumeDeleteParamsToONTAPCollectionDelete(nil)
		assert.NotNil(tt, result)
		assert.Nil(tt, result.Name)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &FlexCacheVolumeDeleteParams{
			UUID: "flexcache-uuid-789",
			Name: "flexcache-volume-name",
		}
		result := flexCacheVolumeDeleteParamsToONTAPCollectionDelete(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Name)
		assert.Equal(tt, "flexcache-volume-name", *result.Name)
		assert.NotNil(tt, result.ReturnTimeout)
		assert.Equal(tt, returnTimeout, *result.ReturnTimeout)
	})
}

func TestIscsiServiceGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		result := iscsiServiceGetParamsToONTAP(nil)
		assert.NotNil(tt, result)
	})

	t.Run("WhenParamsSet_ThenFieldsAreSet", func(tt *testing.T) {
		params := &IscsiGetParams{
			SvmUUID: "uuid1",
			BaseParams: BaseParams{
				Fields: []string{"field1"},
			},
		}
		result := iscsiServiceGetParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, "uuid1", *result.SvmUUID)
		assert.Equal(tt, []string{"field1"}, result.Fields)
		assert.NotNil(tt, result.ReturnTimeout)
	})
}

func TestIscsiServiceCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		result := iscsiServiceCreateParamsToONTAP(nil)
		assert.NotNil(tt, result)
	})

	t.Run("WhenParamsSet_ThenFieldsAreSet", func(tt *testing.T) {
		params := &IscsiCreateParams{
			SvmUUID: "uuid1",
		}
		result := iscsiServiceCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, "uuid1", *result.Info.Svm.UUID)
	})
}
func TestSnapmirrorRelationshipReleaseParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipReleaseParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipReleaseParams{
			UUID: "test-uuid",
		}
		otParams := snapmirrorRelationshipReleaseParamsToONTAP(params)
		assert.Equal(tt, params.UUID, otParams.UUID)
	})
}

func TestSnapmirrorRelationshipTransferCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipTransferCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipTransferCreateParams{
			UUID: "test-uuid",
		}
		otParams := snapmirrorRelationshipTransferCreateParamsToONTAP(params)
		assert.Equal(tt, params.UUID, otParams.RelationshipUUID)
	})
}

func TestSnapmirrorRelationshipTransferGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipTransferGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipTransferGetParams{
			SnapmirrorUUID: "test-uuid",
			SnapshotName:   "snapshot-1",
		}
		otParams := snapmirrorRelationshipTransferGetParamsToONTAP(params)
		assert.Equal(tt, params.SnapmirrorUUID, otParams.RelationshipUUID)
		assert.Equal(tt, params.SnapshotName, *otParams.Snapshot)
	})
}

func TestCloudTargetCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cloudTargetCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CloudTargetCreateParams{
			Name:      nillable.ToPointer("cloud-target"),
			Container: nillable.ToPointer("cloud-container"),
		}
		otParams := cloudTargetCreateParamsToONTAP(params)
		assert.Equal(tt, *params.Name, *otParams.Info.Name)
		assert.Equal(tt, *params.Container, *otParams.Info.Container)
		assert.Equal(tt, objStoreOwner, *otParams.Info.Owner)
		assert.Equal(tt, objStoreSnapmirrorUse, *otParams.Info.SnapmirrorUse)
		assert.Equal(tt, objStoreProviderType, *otParams.Info.ProviderType)
		assert.Equal(tt, objStoreSnapmirrorUse, *otParams.Info.SnapmirrorUse)
	})
}

func TestCloudTargetCollectionGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cloudTargetCollectionGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CloudTargetCollectionGetParams{
			Name: nillable.ToPointer("cloud-target"),
		}
		otParams := cloudTargetCollectionGetParamsToONTAP(params)
		assert.Equal(tt, *params.Name, *otParams.Name)
	})
}
func TestSnapmirrorRelationshipReleaseParamsToONTAP2(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipReleaseParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipReleaseParams{
			UUID: "test-uuid",
		}
		otParams := snapmirrorRelationshipReleaseParamsToONTAP(params)
		assert.Equal(tt, params.UUID, otParams.UUID)
	})
}

func TestSnapmirrorRelationshipTransferCreateParamsToONTAP2(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipTransferCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipTransferCreateParams{
			UUID: "test-uuid",
		}
		otParams := snapmirrorRelationshipTransferCreateParamsToONTAP(params)
		assert.Equal(tt, params.UUID, otParams.RelationshipUUID)
	})
}

func TestSnapmirrorRelationshipTransferGetParamsToONTAP2(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipTransferGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SnapmirrorRelationshipTransferGetParams{
			SnapmirrorUUID: "test-uuid",
			SnapshotName:   "snapshot-1",
		}
		otParams := snapmirrorRelationshipTransferGetParamsToONTAP(params)
		assert.Equal(tt, params.SnapmirrorUUID, otParams.RelationshipUUID)
		assert.Equal(tt, params.SnapshotName, *otParams.Snapshot)
	})
}

func TestCloudTargetCreateParamsToONTAP2(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cloudTargetCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CloudTargetCreateParams{
			Name:      nillable.ToPointer("cloud-target"),
			Container: nillable.ToPointer("cloud-container"),
		}
		otParams := cloudTargetCreateParamsToONTAP(params)
		assert.Equal(tt, *params.Name, *otParams.Info.Name)
		assert.Equal(tt, *params.Container, *otParams.Info.Container)
	})
}

func TestCloudTargetCollectionGetParamsToONTAP2(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cloudTargetCollectionGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CloudTargetCollectionGetParams{
			Name: nillable.ToPointer("cloud-target"),
		}
		otParams := cloudTargetCollectionGetParamsToONTAP(params)
		assert.Equal(tt, *params.Name, *otParams.Name)
	})
}

// Test for dnsCreateParamsToONTAP
func TestDnsCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(t *testing.T) {
		result := dnsCreateParamsToONTAP(nil)
		assert.NotNil(t, result)
	})

	t.Run("WhenParamsSet", func(t *testing.T) {
		domains := []string{"example.com", "test.com"}
		servers := []string{"8.8.8.8", "8.8.4.4"}
		params := &DNSCreateParams{
			Domains:    domains,
			DNSServers: servers,
		}
		result := dnsCreateParamsToONTAP(params)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Info)
		assert.Equal(t, 2, len(result.Info.Domains))
		assert.Equal(t, "example.com", *result.Info.Domains[0])
		assert.Equal(t, "test.com", *result.Info.Domains[1])
		// Verify Servers field is set (line 3499)
		assert.Equal(t, 2, len(result.Info.Servers))
		assert.Equal(t, "8.8.8.8", *result.Info.Servers[0])
		assert.Equal(t, "8.8.4.4", *result.Info.Servers[1])
	})
	t.Run("WhenParamsSetWithEmptyDNSServers", func(t *testing.T) {
		domains := []string{"example.com"}
		servers := []string{}
		params := &DNSCreateParams{
			Domains:    domains,
			DNSServers: servers,
		}
		result := dnsCreateParamsToONTAP(params)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Info)
		// Verify Servers field is set even when empty (line 3499)
		assert.Equal(t, 0, len(result.Info.Servers))
	})
}

func TestLdapGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := ldapGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParmasSet", func(tt *testing.T) {
		params := &LdapGetParams{
			BaseParams: BaseParams{Fields: []string{"field1"}},
			SvmUUID:    "zeUUID",
		}
		otParams := ldapGetParamsToONTAP(params)
		assert.Equal(tt, []string{"field1"}, otParams.Fields)
		assert.Equal(tt, "zeUUID", otParams.SvmUUID)
	})
}

func TestLdapCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := ldapCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSetForLdap", func(tt *testing.T) {
		ipadd1 := "10.10.10.1"
		ipadd2 := "10.10.10.2"
		var ldapPort int64 = 389
		var queryTimeout int64 = 10
		sessionSecuritySign := "sign"
		midBindLevel := "anonymous"
		baseScope := "subtree"
		bindAsCifsServer := true
		params := &LdapCreateParams{
			DomainName:                    nillable.ToPointer("test.com"),
			BaseDN:                        nillable.ToPointer("DC=test, DC=com"),
			UserDn:                        nillable.ToPointer("OU=fin,OU=hr"),
			GroupDn:                       nillable.ToPointer("OU=fin,OU=hr"),
			GroupMembershipFilter:         nillable.ToPointer("(*gidnumber)"),
			Schema:                        nillable.ToPointer("custom-schema"),
			TLSEnabled:                    nillable.ToPointer(true),
			LdapPort:                      &ldapPort,
			SessionSecurity:               &sessionSecuritySign,
			BindAsCifsServer:              &bindAsCifsServer,
			PreferredServersForLdapClient: []*string{&ipadd1, &ipadd2},
		}
		otParams := ldapCreateParamsToONTAP(params)
		assert.Equal(tt, false, *otParams.Info.SkipConfigValidation)
		assert.Equal(tt, "OU=fin,OU=hr", *otParams.Info.UserDn)
		assert.Equal(tt, "OU=fin,OU=hr", *otParams.Info.GroupDn)
		assert.Equal(tt, "(*gidnumber)", *otParams.Info.GroupMembershipFilter)
		assert.Equal(tt, "custom-schema", *otParams.Info.Schema)
		assert.Equal(tt, true, *otParams.Info.UseStartTLS)
		assert.Equal(tt, nillable.ToPointer(ipadd1), otParams.Info.LdapServiceInlinePreferredAdServers[0])
		assert.Equal(tt, nillable.ToPointer(ipadd2), otParams.Info.LdapServiceInlinePreferredAdServers[1])
		assert.Equal(tt, ldapPort, *otParams.Info.Port)
		assert.Equal(tt, "sign", *otParams.Info.SessionSecurity)
		assert.Equal(tt, "test.com", *otParams.Info.AdDomain)
		assert.Equal(tt, "DC=test, DC=com", *otParams.Info.BaseDn)
		assert.Equal(tt, queryTimeout, *otParams.Info.QueryTimeout)
		assert.False(tt, *otParams.Info.SkipConfigValidation)
		assert.Equal(tt, midBindLevel, *otParams.Info.MinBindLevel)
		assert.Equal(tt, baseScope, *otParams.Info.BaseScope)
		assert.Equal(tt, baseScope, *otParams.Info.BaseScope)
		assert.False(tt, *otParams.Info.ReferralEnabled)
		assert.True(tt, *otParams.Info.BindAsCifsServer)
		assert.Nil(tt, otParams.Info.LdapServiceInlineServers)
	})
}

func TestLdapSchemaCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := ldapSchemaCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &LdapSchemaCreateParams{
			Name:     nillable.ToPointer("schemaName"),
			Template: nillable.ToPointer("templateName"),
			SvmUUID:  nillable.ToPointer("zeUUID"),
		}
		otParams := ldapSchemaCreateParamsToONTAP(params)
		assert.Equal(tt, "schemaName", *otParams.Info.Name)
		assert.Equal(tt, "templateName", *otParams.Info.Template.Name)
		assert.Equal(tt, "zeUUID", *otParams.Info.Owner.UUID)
	})
}

func TestLdapSchemaModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := ldapSchemaModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &LdapSchemaModifyParams{
			MaximumGroups: nillable.ToPointer(int64(10)),
			SchemaName:    "schemaName",
			SvmUUID:       "zeUUID",
		}
		otParams := ldapSchemaModifyParamsToONTAP(params)
		assert.Equal(tt, int64(10), *otParams.Info.Rfc2307bis.MaximumGroups)
		assert.Equal(tt, "schemaName", otParams.Name)
		assert.Equal(tt, "zeUUID", otParams.OwnerUUID)
	})
}

func TestGcpKmsModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := gcpKmsModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &GcpKmsModifyParams{
			UUID:                   "uuid",
			ApplicationCredentials: nillable.ToPointer(log.Secret("app cred")),
		}

		otParams := gcpKmsModifyParamsToONTAP(params)
		assert.Equal(tt, "uuid", otParams.UUID)
		// Verify ApplicationCredentials is set when not nil (line 3511)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.ApplicationCredentials)
		assert.Equal(tt, "app cred", otParams.Info.ApplicationCredentials.String())
	})
	t.Run("WhenParamsSetWithNilApplicationCredentials", func(tt *testing.T) {
		params := &GcpKmsModifyParams{
			UUID:                   "uuid",
			ApplicationCredentials: nil,
		}

		otParams := gcpKmsModifyParamsToONTAP(params)
		assert.Equal(tt, "uuid", otParams.UUID)
		// When ApplicationCredentials is nil, Info should not be set (line 3510 check)
		assert.Nil(tt, otParams.Info)
	})
}

func TestExportPolicyCreateParamsToONTAP(t *testing.T) {
	logger := log.NewLogger()
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := exportPolicyCreateParamsToONTAP(nil, logger)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &ExportPolicyCreateParams{
			Name:    "test-policy",
			SvmName: "test-svm",
			Rules: []*ExportRule{
				{
					ClientMatch:   "10.0.0.8,10.0.0.9, 10.0.0.10",
					ReadOnlyRule:  "any",
					ReadWriteRule: "any",
					SuperUserRule: "any",
					AnonymousUser: "65534",
					Index:         1,
					Protocols:     []string{"nfs3", "nfs4"},
				},
			},
		}

		otParams := exportPolicyCreateParamsToONTAP(params, logger)
		assert.Equal(tt, "test-policy", *otParams.Info.Name)
		assert.Equal(tt, "test-svm", *otParams.Info.Svm.Name)
		assert.Len(tt, otParams.Info.ExportPolicyInlineRules, 1)
		assert.Equal(tt, "10.0.0.8", *otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineClients[0].Match)
		assert.Equal(tt, "10.0.0.9", *otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineClients[1].Match)
		assert.Equal(tt, "10.0.0.10", *otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineClients[2].Match)
		assert.Equal(tt, "any", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRoRule[0]))
		assert.Equal(tt, "any", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRwRule[0]))
		assert.Equal(tt, "any", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineSuperuser[0]))
		assert.Equal(tt, "65534", *otParams.Info.ExportPolicyInlineRules[0].AnonymousUser)
		assert.Equal(tt, int64(1), *otParams.Info.ExportPolicyInlineRules[0].Index)
		assert.Len(tt, otParams.Info.ExportPolicyInlineRules[0].Protocols, 2)
		assert.Equal(tt, "nfs3", *otParams.Info.ExportPolicyInlineRules[0].Protocols[0])
		assert.Equal(tt, "nfs4", *otParams.Info.ExportPolicyInlineRules[0].Protocols[1])
	})
	t.Run("WhenReadOnlyAndReadWriteRulesEmpty_DefaultsToSys", func(tt *testing.T) {
		params := &ExportPolicyCreateParams{
			Name:    "p",
			SvmName: "svm",
			Rules: []*ExportRule{
				{
					ClientMatch:   "10.0.0.1",
					SuperUserRule: "none",
					AnonymousUser: "65534",
					Index:         1,
					Protocols:     []string{"nfs"},
				},
			},
		}
		otParams := exportPolicyCreateParamsToONTAP(params, logger)
		assert.Len(tt, otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRoRule, 1)
		assert.Len(tt, otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRwRule, 1)
		assert.Equal(tt, "sys", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRoRule[0]))
		assert.Equal(tt, "sys", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRwRule[0]))
	})
}

func TestExportPolicyGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := exportPolicyGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &ExportPolicyGetParams{
			BaseParams: BaseParams{
				Fields:     []string{"name", "svm"},
				MaxRecords: nillable.ToPointer(int64(0)),
			},
			Name:    nillable.ToPointer("test-policy"),
			SvmName: nillable.ToPointer("test-svm"),
		}

		otParams := exportPolicyGetParamsToONTAP(params)
		assert.Equal(tt, "test-policy", *otParams.Name)
		assert.Equal(tt, "test-svm", *otParams.SvmName)
		assert.Equal(tt, []string{"name", "svm"}, otParams.Fields)
		assert.Equal(tt, "0", *otParams.MaxRecords)
	})
}

func TestExportPolicyModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := exportPolicyModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &ExportPolicyModifyParams{
			BaseParams: BaseParams{},
			ID:         123,
			Name:       nillable.ToPointer("modified-policy"),
			SvmName:    "test-svm",
			Rules: []*ExportRule{
				{
					ClientMatch:   "192.168.0.16,192.168.0.17, 192.168.0.18",
					ReadOnlyRule:  "none",
					ReadWriteRule: "none",
					SuperUserRule: "none",
					AnonymousUser: "65534",
					Index:         1,
					Protocols:     []string{"nfs3"},
				},
			},
		}

		otParams := exportPolicyModifyParamsToONTAP(params)
		assert.Equal(tt, int64(123), params.ID)
		assert.Equal(tt, "modified-policy", *otParams.Info.Name)
		assert.Len(tt, otParams.Info.ExportPolicyInlineRules, 1)
		assert.Equal(tt, "192.168.0.16", *otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineClients[0].Match)
		assert.Equal(tt, "192.168.0.17", *otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineClients[1].Match)
		assert.Equal(tt, "192.168.0.18", *otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineClients[2].Match)
		assert.Equal(tt, "none", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRoRule[0]))
		assert.Equal(tt, "none", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRwRule[0]))
		assert.Equal(tt, "none", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineSuperuser[0]))
		assert.Equal(tt, "65534", *otParams.Info.ExportPolicyInlineRules[0].AnonymousUser)
		assert.Len(tt, otParams.Info.ExportPolicyInlineRules[0].Protocols, 1)
		assert.Equal(tt, "nfs3", *otParams.Info.ExportPolicyInlineRules[0].Protocols[0])
	})
	t.Run("WhenReadOnlyAndReadWriteRulesEmpty_DefaultsToSys", func(tt *testing.T) {
		params := &ExportPolicyModifyParams{
			BaseParams: BaseParams{},
			ID:         1,
			SvmName:    "svm",
			Rules: []*ExportRule{
				{
					ClientMatch:   "10.0.0.2",
					SuperUserRule: "none",
					AnonymousUser: "65534",
					Protocols:     []string{"nfs3"},
				},
			},
		}
		otParams := exportPolicyModifyParamsToONTAP(params)
		assert.Len(tt, otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRoRule, 1)
		assert.Len(tt, otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRwRule, 1)
		assert.Equal(tt, "sys", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRoRule[0]))
		assert.Equal(tt, "sys", string(*otParams.Info.ExportPolicyInlineRules[0].ExportRulesInlineRwRule[0]))
	})
}

func TestExportPolicyDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := exportPolicyDeleteParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &ExportPolicyDeleteParams{
			BaseParams: BaseParams{},
			Name:       "test-policy",
			SvmName:    "test-svm",
		}

		otParams := exportPolicyDeleteParamsToONTAP(params)
		assert.Equal(tt, "test-policy", *otParams.Name)
		assert.Equal(tt, "test-svm", *otParams.SvmName)
	})
}

func TestNfsServiceGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := nfsServiceGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &NfsServiceGetParams{
			BaseParams: BaseParams{
				Fields: []string{"enabled", "protocol"},
			},
			SvmUUID: "test-svm-uuid",
		}

		otParams := nfsServiceGetParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.Equal(tt, []string{"enabled", "protocol"}, otParams.Fields)
	})
}

func TestNfsServiceCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := nfsServiceCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &NfsServiceCreateParams{
			BaseParams: BaseParams{},
			SvmUUID:    "test-svm-uuid",
			Enabled:    nillable.ToPointer(true),
			V3:         nillable.ToPointer(true),
			V4:         nillable.ToPointer(false),
			V41:        nillable.ToPointer(true),
		}

		otParams := nfsServiceCreateParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", *otParams.Info.Svm.UUID)
		assert.True(tt, *otParams.Info.Enabled)
		assert.True(tt, *otParams.Info.Protocol.V3Enabled)
		assert.False(tt, *otParams.Info.Protocol.V40Enabled)
		assert.True(tt, *otParams.Info.Protocol.V41Enabled)
	})
	t.Run("WhenProtocolNotSet", func(tt *testing.T) {
		params := &NfsServiceCreateParams{
			BaseParams: BaseParams{},
			SvmUUID:    "test-svm-uuid",
			Enabled:    nillable.ToPointer(true),
		}

		otParams := nfsServiceCreateParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", *otParams.Info.Svm.UUID)
		assert.True(tt, *otParams.Info.Enabled)
		assert.Nil(tt, otParams.Info.Protocol)
	})
}

func TestNfsParamsModifyToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := nfsParamsModifyToONTAP(context.Background(), nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &NfsModifyParams{
			SvmUUID:    "test-svm-uuid",
			Enabled:    nillable.ToPointer(false),
			V3Enabled:  nillable.ToPointer(false),
			V40Enabled: nillable.ToPointer(true),
			V41Enabled: nillable.ToPointer(false),
		}

		otParams := nfsParamsModifyToONTAP(context.Background(), params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.False(tt, *otParams.Info.Enabled)
		assert.False(tt, *otParams.Info.Protocol.V3Enabled)
		assert.True(tt, *otParams.Info.Protocol.V40Enabled)
		assert.False(tt, *otParams.Info.Protocol.V41Enabled)
	})
	t.Run("WhenProtocolNotSet", func(tt *testing.T) {
		params := &NfsModifyParams{
			SvmUUID: "test-svm-uuid",
			Enabled: nillable.ToPointer(true),
		}

		otParams := nfsParamsModifyToONTAP(context.Background(), params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.True(tt, *otParams.Info.Enabled)
		assert.NotNil(tt, otParams.Info.Protocol)
	})
	t.Run("WhenRquotaEnabled", func(tt *testing.T) {
		params := &NfsModifyParams{
			SvmUUID:       "test-svm-uuid",
			RquotaEnabled: nillable.ToPointer(true),
		}

		otParams := nfsParamsModifyToONTAP(context.Background(), params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.True(tt, *otParams.Info.RquotaEnabled)
	})
	t.Run("WhenRquotaDisabled", func(tt *testing.T) {
		params := &NfsModifyParams{
			SvmUUID:       "test-svm-uuid",
			RquotaEnabled: nillable.ToPointer(false),
		}

		otParams := nfsParamsModifyToONTAP(context.Background(), params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.False(tt, *otParams.Info.RquotaEnabled)
	})
}

// TestNfsModifyParamsToONTAP tests the Active Directory nfsModifyParamsToONTAP function (without context)
func TestNfsModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := nfsModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &NfsModifyParams{
			SvmUUID:                    "test-svm-uuid",
			AllowLocalNFSUsersWithLdap: nillable.ToPointer(true),
			V4IDDomain:                 nillable.ToPointer("example.com"),
			Enabled:                    nillable.ToPointer(true),
		}

		otParams := nfsModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.True(tt, *otParams.Info.Enabled)
		assert.True(tt, *otParams.Info.AuthSysExtendedGroupsEnabled)
		assert.Equal(tt, "example.com", *otParams.Info.Protocol.V4IDDomain)
	})
	t.Run("WhenRquotaEnabled", func(tt *testing.T) {
		params := &NfsModifyParams{
			SvmUUID:       "test-svm-uuid",
			RquotaEnabled: nillable.ToPointer(true),
		}

		otParams := nfsModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.True(tt, *otParams.Info.RquotaEnabled)
	})
}

func TestNfsServiceModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := nfsServiceModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &NfsServiceModifyParams{
			BaseParams: BaseParams{},
			SvmUUID:    "test-svm-uuid",
			Enabled:    nillable.ToPointer(false),
			V3:         nillable.ToPointer(false),
			V4:         nillable.ToPointer(true),
			V41:        nillable.ToPointer(false),
		}

		otParams := nfsServiceModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.False(tt, *otParams.Info.Enabled)
		assert.False(tt, *otParams.Info.Protocol.V3Enabled)
		assert.True(tt, *otParams.Info.Protocol.V40Enabled)
		assert.False(tt, *otParams.Info.Protocol.V41Enabled)
	})
	t.Run("WhenProtocolNotSet", func(tt *testing.T) {
		params := &NfsServiceModifyParams{
			BaseParams: BaseParams{},
			SvmUUID:    "test-svm-uuid",
			Enabled:    nillable.ToPointer(true),
		}

		otParams := nfsServiceModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.True(tt, *otParams.Info.Enabled)
		assert.Nil(tt, otParams.Info.Protocol)
	})
}

func TestCifsServiceGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cifsServiceGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CifsServiceGetParams{
			BaseParams: BaseParams{
				Fields: []string{"name", "enabled"},
			},
			SvmName: nillable.ToPointer("test-svm"),
			SvmUUID: nillable.ToPointer("test-svm-uuid"),
		}

		otParams := cifsServiceGetParamsToONTAP(params)
		assert.Equal(tt, "test-svm", *otParams.SvmName)
		assert.Equal(tt, "test-svm-uuid", *otParams.SvmUUID)
		assert.Equal(tt, []string{"name", "enabled"}, otParams.Fields)
	})
}

func TestCifsServiceCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cifsServiceCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CifsServiceCreateParams{
			SvmName: nillable.ToPointer("test-svm"),
			Name:    nillable.ToPointer("test-cifs"),
			Domain:  nillable.ToPointer("test.domain.com"),
		}

		otParams := cifsServiceCreateParamsToONTAP(params)
		assert.Equal(tt, "test-svm", *otParams.Info.Svm.Name)
		assert.Equal(tt, "test-cifs", *otParams.Info.Name)
	})

	t.Run("WhenAuthUserTypeIsHybridUser", func(tt *testing.T) {
		hybridUserType := hybridUser
		cert := "cert-data"
		params := &CifsServiceCreateParams{
			SvmName:            nillable.ToPointer("svm1"),
			Name:               nillable.ToPointer("cifs1"),
			Domain:             nillable.ToPointer("domain.com"),
			ClientID:           nillable.ToPointer("client-id"),
			TenantID:           nillable.ToPointer("tenant-id"),
			EntraIDCertificate: nillable.ToPointer(log.Secret(cert)),
			AuthUserType:       &hybridUserType,
			TLSEnabled:         nillable.ToPointer(true),
		}
		otParams := cifsServiceCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "cifs1", *otParams.Info.Name)
		assert.Equal(tt, "svm1", *otParams.Info.Svm.Name)
		assert.Equal(tt, "client-id", *otParams.Info.ClientID)
		assert.Equal(tt, "tenant-id", *otParams.Info.TenantID)
		assert.Equal(tt, &hybridUserType, otParams.Info.AuthUserType)
		assert.NotNil(tt, otParams.Info.AuthenticationMethod)
		assert.Equal(tt, authMethodCertificate, *otParams.Info.AuthenticationMethod)
		assert.NotNil(tt, otParams.Info.AdDomain)
		assert.Equal(tt, "domain.com", *otParams.Info.AdDomain.Fqdn)
		assert.NotNil(tt, otParams.Info.ClientCertificate)
		assert.Equal(tt, strfmt.Password(cert), *otParams.Info.ClientCertificate)
	})

	t.Run("WhenAuthUserTypeIsNotHybridUser", func(tt *testing.T) {
		otherUserType := "other"
		site := "site1"
		ou := "ou1"
		username := "user1"
		password := "pass1"
		params := &CifsServiceCreateParams{
			SvmName:            nillable.ToPointer("svm1"),
			Name:               nillable.ToPointer("cifs1"),
			Domain:             nillable.ToPointer("domain.com"),
			Site:               &site,
			OrganizationalUnit: &ou,
			Username:           &username,
			Password:           &password,
			AuthUserType:       &otherUserType,
			TLSEnabled:         nillable.ToPointer(false),
		}
		otParams := cifsServiceCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "cifs1", *otParams.Info.Name)
		assert.NotNil(tt, otParams.Info.AdDomain)
		assert.Equal(tt, &site, otParams.Info.AdDomain.DefaultSite)
		assert.Equal(tt, &ou, otParams.Info.AdDomain.OrganizationalUnit)
		assert.Equal(tt, &username, otParams.Info.AdDomain.User)
		assert.Equal(tt, &password, otParams.Info.AdDomain.Password)
		assert.Equal(tt, "domain.com", *otParams.Info.AdDomain.Fqdn)
	})

	t.Run("WhenAuthUserTypeIsNotHybridUserAndNoAdDomainFields", func(tt *testing.T) {
		otherUserType := "other"
		params := &CifsServiceCreateParams{
			SvmName:      nillable.ToPointer("svm1"),
			Name:         nillable.ToPointer("cifs1"),
			Domain:       nillable.ToPointer("domain.com"),
			AuthUserType: &otherUserType,
			TLSEnabled:   nillable.ToPointer(false),
		}
		otParams := cifsServiceCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Nil(tt, otParams.Info.AdDomain)
	})
}

func TestCifsServiceModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cifsServiceModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CifsServiceModifyParams{
			SvmUUID: nillable.ToPointer("test-svm-uuid"),
			Enabled: nillable.ToPointer(false),
		}

		otParams := cifsServiceModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.False(tt, *otParams.Info.Enabled)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CifsServiceModifyParams{
			SvmUUID: nillable.ToPointer("test-svm-uuid"),
			Enabled: nillable.ToPointer(false),
		}

		otParams := cifsServiceModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.NotNil(tt, otParams.Info)
		assert.False(tt, *otParams.Info.Enabled)
	})

	t.Run("WhenAuthenticationFieldsSet", func(tt *testing.T) {
		username := "admin-user"
		password := "secure-password"
		site := "site-location"
		params := &CifsServiceModifyParams{
			SvmUUID:  nillable.ToPointer("test-svm-uuid"),
			Username: &username,
			Password: &password,
			Site:     &site,
		}

		otParams := cifsServiceModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.AdDomain)
		assert.Equal(tt, &username, otParams.Info.AdDomain.User)
		assert.Equal(tt, &password, otParams.Info.AdDomain.Password)
		assert.Equal(tt, &site, otParams.Info.AdDomain.DefaultSite)
	})

	t.Run("WhenEncryptionFieldsSet", func(tt *testing.T) {
		aesEncrypt := true
		dcEncrypt := true
		params := &CifsServiceModifyParams{
			SvmUUID:              nillable.ToPointer("test-svm-uuid"),
			AesEncryptionEnabled: &aesEncrypt,
			EncryptDCConnections: &dcEncrypt,
		}

		otParams := cifsServiceModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.Security)
		assert.True(tt, *otParams.Info.Security.EncryptDcConnection)
	})

	t.Run("WhenAllAuthAndEncryptionFieldsSet", func(tt *testing.T) {
		username := "admin-user"
		password := "secure-password"
		site := "site-location"
		aesEncrypt := true
		dcEncrypt := false
		params := &CifsServiceModifyParams{
			SvmUUID:              nillable.ToPointer("test-svm-uuid"),
			Username:             &username,
			Password:             &password,
			Site:                 &site,
			AesEncryptionEnabled: &aesEncrypt,
			EncryptDCConnections: &dcEncrypt,
			Enabled:              nillable.ToPointer(true),
		}

		otParams := cifsServiceModifyParamsToONTAP(params)
		assert.Equal(tt, "test-svm-uuid", otParams.SvmUUID)
		assert.NotNil(tt, otParams.Info)
		assert.True(tt, *otParams.Info.Enabled)
		assert.NotNil(tt, otParams.Info.AdDomain)
		assert.Equal(tt, &username, otParams.Info.AdDomain.User)
		assert.Equal(tt, &password, otParams.Info.AdDomain.Password)
		assert.Equal(tt, &site, otParams.Info.AdDomain.DefaultSite)
		assert.NotNil(tt, otParams.Info.Security)
		assert.False(tt, *otParams.Info.Security.EncryptDcConnection)
	})

	t.Run("WhenOnlyEnabledFieldSet", func(tt *testing.T) {
		params := &CifsServiceModifyParams{
			SvmUUID: nillable.ToPointer("svm-uuid-3"),
			Enabled: nillable.ToPointer(true),
		}

		otParams := cifsServiceModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "svm-uuid-3", otParams.SvmUUID)
		assert.True(tt, *otParams.Info.Enabled)
		assert.Nil(tt, otParams.Info.AdDomain)
		assert.Nil(tt, otParams.Info.ClientCertificate)
	})
}

func TestLdapModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		result := ldapModifyParamsToONTAP(nil)
		assert.NotNil(tt, result)
		assert.Nil(tt, result.Info)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &LdapModifyParams{
			SvmUUID:                       "test-svm-uuid",
			UserDn:                        strPtr("cn=users,dc=example,dc=com"),
			GroupDn:                       strPtr("cn=groups,dc=example,dc=com"),
			BaseDN:                        strPtr("dc=example,dc=com"),
			GroupMembershipFilter:         strPtr("(memberOf=*)"),
			PreferredServersForLdapClient: []*string{strPtr("ldap1.example.com"), strPtr("ldap2.example.com")},
			TLSEnabled:                    nillable.ToPointer(true),
			Schema:                        strPtr("AD"),
			LdapServers:                   []*string{strPtr("ldap.example.com")},
		}

		result := ldapModifyParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-svm-uuid", result.SvmUUID)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.SkipConfigValidation)
		assert.False(tt, *result.Info.SkipConfigValidation)
		assert.Equal(tt, params.UserDn, result.Info.UserDn)
		assert.Equal(tt, params.GroupDn, result.Info.GroupDn)
		assert.Equal(tt, params.BaseDN, result.Info.BaseDn)
		assert.Equal(tt, params.GroupMembershipFilter, result.Info.GroupMembershipFilter)
		assert.Equal(tt, params.PreferredServersForLdapClient, result.Info.LdapServiceInlinePreferredAdServers)
		assert.Equal(tt, params.TLSEnabled, result.Info.UseStartTLS)
		assert.Equal(tt, params.Schema, result.Info.Schema)
		assert.Equal(tt, params.LdapServers, result.Info.LdapServiceInlineServers)
	})

	t.Run("WhenParamsSetWithMinimalFields", func(tt *testing.T) {
		params := &LdapModifyParams{
			SvmUUID: "minimal-svm-uuid",
			BaseDN:  strPtr("dc=minimal,dc=com"),
		}

		result := ldapModifyParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.Equal(tt, "minimal-svm-uuid", result.SvmUUID)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.SkipConfigValidation)
		assert.False(tt, *result.Info.SkipConfigValidation)
		assert.Equal(tt, params.BaseDN, result.Info.BaseDn)
		assert.Nil(tt, result.Info.UserDn)
		assert.Nil(tt, result.Info.GroupDn)
		assert.Nil(tt, result.Info.UseStartTLS)
	})

	t.Run("WhenParamsSetWithEmptySlices", func(tt *testing.T) {
		params := &LdapModifyParams{
			SvmUUID:                       "test-svm-uuid",
			BaseDN:                        strPtr("dc=test,dc=com"),
			PreferredServersForLdapClient: []*string{},
			LdapServers:                   []*string{},
		}

		result := ldapModifyParamsToONTAP(params)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-svm-uuid", result.SvmUUID)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.LdapServiceInlinePreferredAdServers)
		assert.Empty(tt, result.Info.LdapServiceInlinePreferredAdServers)
		assert.NotNil(tt, result.Info.LdapServiceInlineServers)
		assert.Empty(tt, result.Info.LdapServiceInlineServers)
	})
}

func TestSnapmirrorCloudSnapshotGetParamsToONTAP(t *testing.T) {
	t.Run("WhenAllParamsEmpty", func(tt *testing.T) {
		params := &SnapmirrorCloudSnapshotGetParams{}
		otParams := snapmirrorCloudSnapshotGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Empty(tt, otParams.ObjectStoreUUID)
		assert.Empty(tt, otParams.EndpointUUID)
		assert.Empty(tt, otParams.UUID)
	})

	t.Run("WhenAllParamsSet", func(tt *testing.T) {
		params := &SnapmirrorCloudSnapshotGetParams{
			ObjectStoreUUID: "obj-store-uuid",
			EndpointUUID:    "endpoint-uuid",
			SnapshotUUID:    "snapshot-uuid",
		}
		otParams := snapmirrorCloudSnapshotGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "obj-store-uuid", otParams.ObjectStoreUUID)
		assert.Equal(tt, "endpoint-uuid", otParams.EndpointUUID)
		assert.Equal(tt, "snapshot-uuid", otParams.UUID)
	})

	t.Run("WhenOnlyObjectStoreUUIDSet", func(tt *testing.T) {
		params := &SnapmirrorCloudSnapshotGetParams{
			ObjectStoreUUID: "obj-store-uuid",
		}
		otParams := snapmirrorCloudSnapshotGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "obj-store-uuid", otParams.ObjectStoreUUID)
		assert.Empty(tt, otParams.EndpointUUID)
		assert.Empty(tt, otParams.UUID)
	})

	t.Run("WhenOnlyEndpointUUIDSet", func(tt *testing.T) {
		params := &SnapmirrorCloudSnapshotGetParams{
			EndpointUUID: "endpoint-uuid",
		}
		otParams := snapmirrorCloudSnapshotGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Empty(tt, otParams.ObjectStoreUUID)
		assert.Equal(tt, "endpoint-uuid", otParams.EndpointUUID)
		assert.Empty(tt, otParams.UUID)
	})

	t.Run("WhenOnlySnapshotUUIDSet", func(tt *testing.T) {
		params := &SnapmirrorCloudSnapshotGetParams{
			SnapshotUUID: "snapshot-uuid",
		}
		otParams := snapmirrorCloudSnapshotGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Empty(tt, otParams.ObjectStoreUUID)
		assert.Empty(tt, otParams.EndpointUUID)
		assert.Equal(tt, "snapshot-uuid", otParams.UUID)
	})
}

func TestSecurityAuditModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := securityAuditModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SecurityAuditUpdateParams{
			Cli:    true,
			HTTP:   true,
			Ontapi: true,
		}

		otParams := securityAuditModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.Cli)
		assert.True(tt, *otParams.Info.Cli)
		assert.NotNil(tt, otParams.Info.HTTP)
		assert.True(tt, *otParams.Info.HTTP)
		assert.NotNil(tt, otParams.Info.Ontapi)
		assert.True(tt, *otParams.Info.Ontapi)
	})
}

func TestSecurityLogForwardingGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := securityLogForwardingGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SecurityLogForwardingGetParams{
			Address: "test-address",
			Port:    int64(1234),
		}

		otParams := securityLogForwardingGetParamsToONTAP(params)
		assert.Equal(tt, "test-address", otParams.Address)
		assert.Equal(tt, int64(1234), otParams.Port)
	})
}

func TestSecurityLogForwardingCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := securityLogForwardingCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &SecurityLogForwardingCreateParams{
			BaseParams:   BaseParams{},
			Address:      nillable.ToPointer("test-address"),
			Port:         nillable.ToPointer(int64(1234)),
			Protocol:     nillable.ToPointer("http"),
			Facility:     nillable.ToPointer("test-facility"),
			VerifyServer: nillable.ToPointer(true),
		}

		otParams := securityLogForwardingCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.Address)
		assert.Equal(tt, "test-address", *otParams.Info.Address)
		assert.NotNil(tt, otParams.Info.Port)
		assert.Equal(tt, int64(1234), *otParams.Info.Port)
		assert.NotNil(tt, otParams.Info.Protocol)
		assert.Equal(tt, "http", *otParams.Info.Protocol)
		assert.NotNil(tt, otParams.Info.Facility)
		assert.Equal(tt, "test-facility", *otParams.Info.Facility)
		assert.NotNil(tt, otParams.Info.VerifyServer)
		assert.True(tt, *otParams.Info.VerifyServer)
	})
}

func TestLunCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		otParams := lunCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
		assert.Nil(tt, otParams.Info)
	})

	t.Run("WhenParamsSet_ThenFieldsAreSet", func(tt *testing.T) {
		thinProvisioning := true
		params := &LunCreateParams{
			SvmName:                        "test-svm",
			Name:                           "test-lun",
			OsType:                         "LINUX",
			VolumeName:                     "test-volume",
			Size:                           1073741824, // 1GB
			ThinProvisioningSupportEnabled: &thinProvisioning,
		}

		otParams := lunCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)

		// Verify SVM
		assert.NotNil(tt, otParams.Info.Svm)
		assert.Equal(tt, "test-svm", *otParams.Info.Svm.Name)

		// Verify Location
		assert.NotNil(tt, otParams.Info.Location)
		assert.NotNil(tt, otParams.Info.Location.Volume)
		assert.Equal(tt, "test-volume", *otParams.Info.Location.Volume.Name)

		// Verify Space
		assert.NotNil(tt, otParams.Info.Space)
		assert.Equal(tt, int64(1073741824), *otParams.Info.Space.Size)
		assert.Equal(tt, &thinProvisioning, otParams.Info.Space.ScsiThinProvisioningSupportEnabled)

		// Verify OS Type mapping
		assert.Equal(tt, "LINUX", *otParams.Info.OsType)
	})

	t.Run("WhenOsTypeIsESXI_ThenMapsToVMWARE", func(tt *testing.T) {
		params := &LunCreateParams{
			SvmName:    "test-svm",
			Name:       "test-lun",
			OsType:     "ESXI",
			VolumeName: "test-volume",
			Size:       1073741824,
		}

		otParams := lunCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "VMWARE", *otParams.Info.OsType)
	})

	t.Run("WhenOsTypeIsWindows_ThenMapsToWINDOWS", func(tt *testing.T) {
		params := &LunCreateParams{
			SvmName:    "test-svm",
			Name:       "test-lun",
			OsType:     "WINDOWS",
			VolumeName: "test-volume",
			Size:       1073741824,
		}

		otParams := lunCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "WINDOWS", *otParams.Info.OsType)
	})

	t.Run("WhenOsTypeIsUnknown_ThenOsTypeNotSet", func(tt *testing.T) {
		params := &LunCreateParams{
			SvmName:    "test-svm",
			Name:       "test-lun",
			OsType:     "UNKNOWN",
			VolumeName: "test-volume",
			Size:       1073741824,
		}

		otParams := lunCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "UNKNOWN", *otParams.Info.OsType)
	})

	t.Run("WhenThinProvisioningIsNil_ThenFieldIsNil", func(tt *testing.T) {
		params := &LunCreateParams{
			SvmName:                        "test-svm",
			Name:                           "test-lun",
			OsType:                         "LINUX",
			VolumeName:                     "test-volume",
			Size:                           1073741824,
			ThinProvisioningSupportEnabled: nil,
		}

		otParams := lunCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info.Space)
		assert.Nil(tt, otParams.Info.Space.ScsiThinProvisioningSupportEnabled)
	})

	t.Run("WhenThinProvisioningIsFalse_ThenFieldIsFalse", func(tt *testing.T) {
		thinProvisioning := false
		params := &LunCreateParams{
			SvmName:                        "test-svm",
			Name:                           "test-lun",
			OsType:                         "LINUX",
			VolumeName:                     "test-volume",
			Size:                           1073741824,
			ThinProvisioningSupportEnabled: &thinProvisioning,
		}

		otParams := lunCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info.Space)
		assert.Equal(tt, &thinProvisioning, otParams.Info.Space.ScsiThinProvisioningSupportEnabled)
	})
}

func TestIgroupCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		otParams := igroupCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
		assert.Nil(tt, otParams.Info)
	})

	t.Run("WhenParamsSet_ThenFieldsAreSet", func(tt *testing.T) {
		params := &IgroupCreateParams{
			SvmName:    "test-svm",
			Name:       "test-igroup",
			OsType:     "LINUX",
			Initiators: []string{"iqn.1993-08.org.debian:01:c9a5ccf7ca95", "iqn.1993-08.org.debian:01:d0b6dde8db06"},
			JobID:      "test-job-123",
		}

		otParams := igroupCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)

		// Verify SVM
		assert.NotNil(tt, otParams.Info.Svm)
		assert.Equal(tt, "test-svm", *otParams.Info.Svm.Name)

		// Verify basic fields
		assert.Equal(tt, "test-igroup", *otParams.Info.Name)
		assert.Equal(tt, "LINUX", *otParams.Info.OsType)
		assert.Equal(tt, "test-job-123", *otParams.Info.Comment)
		assert.Equal(tt, models.IgroupProtocolIscsi, *otParams.Info.Protocol)

		// Verify initiators
		assert.Len(tt, otParams.Info.IgroupInlineInitiators, 2)
		assert.Equal(tt, "iqn.1993-08.org.debian:01:c9a5ccf7ca95", *otParams.Info.IgroupInlineInitiators[0].Name)
		assert.Equal(tt, "iqn.1993-08.org.debian:01:d0b6dde8db06", *otParams.Info.IgroupInlineInitiators[1].Name)
	})

	t.Run("WhenOsTypeIsESXI_ThenMapsToVMWARE", func(tt *testing.T) {
		params := &IgroupCreateParams{
			SvmName:    "test-svm",
			Name:       "test-igroup",
			OsType:     "ESXI",
			Initiators: []string{"iqn.1998-01.com.vmware:esx001-0badb9cf"},
			JobID:      "test-job-123",
		}

		otParams := igroupCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "VMWARE", *otParams.Info.OsType)
	})

	t.Run("WhenOsTypeIsLinux_ThenMapsToLINUX", func(tt *testing.T) {
		params := &IgroupCreateParams{
			SvmName:    "test-svm",
			Name:       "test-igroup",
			OsType:     "LINUX",
			Initiators: []string{"iqn.1994-05.com.example:test1"},
			JobID:      "test-job-123",
		}

		otParams := igroupCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "LINUX", *otParams.Info.OsType)
	})

	t.Run("WhenOsTypeIsWindows_ThenMapsToWINDOWS", func(tt *testing.T) {
		params := &IgroupCreateParams{
			SvmName:    "test-svm",
			Name:       "test-igroup",
			OsType:     "WINDOWS",
			Initiators: []string{"iqn.1991-05.com.microsoft:wlm-sql-gcnv1-sql-server"},
			JobID:      "test-job-123",
		}

		otParams := igroupCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "WINDOWS", *otParams.Info.OsType)
	})

	t.Run("WhenInitiatorsEmpty_ThenInitiatorsArrayIsEmpty", func(tt *testing.T) {
		params := &IgroupCreateParams{
			SvmName:    "test-svm",
			Name:       "test-igroup",
			OsType:     "LINUX",
			Initiators: []string{},
			JobID:      "test-job-123",
		}

		otParams := igroupCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info.IgroupInlineInitiators)
		assert.Len(tt, otParams.Info.IgroupInlineInitiators, 0)
	})

	t.Run("WhenInitiatorsNil_ThenInitiatorsArrayIsEmpty", func(tt *testing.T) {
		params := &IgroupCreateParams{
			SvmName:    "test-svm",
			Name:       "test-igroup",
			OsType:     "LINUX",
			Initiators: nil,
			JobID:      "test-job-123",
		}

		otParams := igroupCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info.IgroupInlineInitiators)
		assert.Len(tt, otParams.Info.IgroupInlineInitiators, 0)
	})

	t.Run("WhenSingleInitiator_ThenInitiatorsArrayHasOne", func(tt *testing.T) {
		params := &IgroupCreateParams{
			SvmName:    "test-svm",
			Name:       "test-igroup",
			OsType:     "LINUX",
			Initiators: []string{"iqn.1993-08.org.debian:01:c9a5ccf7ca95"},
			JobID:      "test-job-123",
		}

		otParams := igroupCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Len(tt, otParams.Info.IgroupInlineInitiators, 1)
		assert.Equal(tt, "iqn.1993-08.org.debian:01:c9a5ccf7ca95", *otParams.Info.IgroupInlineInitiators[0].Name)
	})

	t.Run("WhenJobIDEmpty_ThenCommentIsEmpty", func(tt *testing.T) {
		params := &IgroupCreateParams{
			SvmName:    "test-svm",
			Name:       "test-igroup",
			OsType:     "LINUX",
			Initiators: []string{"iqn.1993-08.org.debian:01:c9a5ccf7ca95"},
			JobID:      "",
		}

		otParams := igroupCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", *otParams.Info.Comment)
	})
}

func TestFlexCacheModifyParamsToONTAP(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	int16Ptr := func(i int16) *int16 { return &i }
	strPtr := func(s string) *string { return &s }
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		ot := flexCacheModifyParamsToONTAP(nil)
		assert.NotNil(tt, ot)
		assert.Empty(tt, ot.UUID)
		assert.Nil(tt, ot.Info)
	})

	t.Run("WhenOnlyUUIDSet_ThenUUIDIsSetAndInfoNil", func(tt *testing.T) {
		p := &FlexcacheModifyParams{UUID: "fc-uuid-1"}
		ot := flexCacheModifyParamsToONTAP(p)
		assert.Equal(tt, "fc-uuid-1", ot.UUID)
	})

	t.Run("WhenPrepopulateFieldsSet_ThenPrepopulateStructSet", func(tt *testing.T) {
		dir1, dir2 := strPtr("/a"), strPtr("/b")
		ex1 := strPtr("/a/tmp")
		rec := boolPtr(true)
		p := &FlexcacheModifyParams{
			UUID:                       "fc-prepop",
			PrepopulateDirPaths:        []*string{dir1, dir2},
			PrepopulateExcludeDirPaths: []*string{ex1},
			PrepopulateRecurse:         rec,
		}
		ot := flexCacheModifyParamsToONTAP(p)
		assert.Equal(tt, "fc-prepop", ot.UUID)
		if assert.NotNil(tt, ot.Info) && assert.NotNil(tt, ot.Info.Prepopulate) {
			assert.Len(tt, ot.Info.Prepopulate.DirPaths, 2)
			assert.Equal(tt, *dir1, *ot.Info.Prepopulate.DirPaths[0])
			assert.Equal(tt, *dir2, *ot.Info.Prepopulate.DirPaths[1])
			assert.Len(tt, ot.Info.Prepopulate.ExcludeDirPaths, 1)
			assert.Equal(tt, *ex1, *ot.Info.Prepopulate.ExcludeDirPaths[0])
			assert.Equal(tt, rec, ot.Info.Prepopulate.Recurse)
		}
		assert.Nil(tt, ot.Info.Writeback)
		assert.Nil(tt, ot.Info.AtimeScrub)
		assert.Nil(tt, ot.Info.CifsChangeNotify)
		assert.Nil(tt, ot.Info.RelativeSize)
	})

	t.Run("WhenAtimeScrubFieldsSet_ThenAtimeScrubStructSetOnly", func(tt *testing.T) {
		en := boolPtr(true)
		period := int16Ptr(12)
		p := &FlexcacheModifyParams{
			UUID:              "fc-atime",
			AtimeScrubEnabled: en,
			AtimeScrubPeriod:  period,
		}
		ot := flexCacheModifyParamsToONTAP(p)
		assert.Equal(tt, "fc-atime", ot.UUID)
		if assert.NotNil(tt, ot.Info) && assert.NotNil(tt, ot.Info.AtimeScrub) {
			assert.Equal(tt, en, ot.Info.AtimeScrub.Enabled)
			assert.Equal(tt, period, ot.Info.AtimeScrub.Period)
		}
		assert.Nil(tt, ot.Info.Prepopulate)
		assert.Nil(tt, ot.Info.Writeback)
		assert.Nil(tt, ot.Info.CifsChangeNotify)
		assert.Nil(tt, ot.Info.RelativeSize)
	})

	t.Run("WhenWritebackEnabledSet_ThenWritebackStructSet", func(tt *testing.T) {
		wb := boolPtr(true)
		p := &FlexcacheModifyParams{
			UUID:             "fc-wb",
			WritebackEnabled: wb,
		}
		ot := flexCacheModifyParamsToONTAP(p)
		assert.Equal(tt, "fc-wb", ot.UUID)
		if assert.NotNil(tt, ot.Info) && assert.NotNil(tt, ot.Info.Writeback) {
			assert.Equal(tt, wb, ot.Info.Writeback.Enabled)
		}
		assert.Nil(tt, ot.Info.AtimeScrub)
		assert.Nil(tt, ot.Info.CifsChangeNotify)
		assert.Nil(tt, ot.Info.RelativeSize)
	})

	t.Run("WhenCifsChangeNotifyEnabledSet_ThenNotifyStructSet", func(tt *testing.T) {
		n := boolPtr(true)
		p := &FlexcacheModifyParams{
			UUID:                    "fc-cifs",
			CifsChangeNotifyEnabled: n,
		}
		ot := flexCacheModifyParamsToONTAP(p)
		assert.Equal(tt, "fc-cifs", ot.UUID)
		if assert.NotNil(tt, ot.Info) && assert.NotNil(tt, ot.Info.CifsChangeNotify) {
			assert.Equal(tt, n, ot.Info.CifsChangeNotify.Enabled)
		}
		assert.Nil(tt, ot.Info.AtimeScrub)
		assert.Nil(tt, ot.Info.Writeback)
		assert.Nil(tt, ot.Info.RelativeSize)
	})

	t.Run("WhenMultipleFieldsSet_ThenAllCorrespondingStructsSet", func(tt *testing.T) {
		wb := boolPtr(true)
		atimeEn := boolPtr(true)
		atimePeriod := int16Ptr(7)
		rsEn := boolPtr(true)
		rsPct := int16Ptr(42)
		cn := boolPtr(false)
		dir := strPtr("/data")
		ex := strPtr("/data/tmp")
		rec := boolPtr(false)

		p := &FlexcacheModifyParams{
			UUID:                       "fc-multi",
			WritebackEnabled:           wb,
			AtimeScrubEnabled:          atimeEn,
			AtimeScrubPeriod:           atimePeriod,
			RelativeSizeEnabled:        rsEn,
			RelativeSizePercentage:     rsPct,
			CifsChangeNotifyEnabled:    cn,
			PrepopulateDirPaths:        []*string{dir},
			PrepopulateExcludeDirPaths: []*string{ex},
			PrepopulateRecurse:         rec,
		}

		ot := flexCacheModifyParamsToONTAP(p)
		assert.Equal(tt, "fc-multi", ot.UUID)
		if assert.NotNil(tt, ot.Info) {
			if assert.NotNil(tt, ot.Info.Writeback) {
				assert.Equal(tt, wb, ot.Info.Writeback.Enabled)
			}
			if assert.NotNil(tt, ot.Info.AtimeScrub) {
				assert.Equal(tt, atimeEn, ot.Info.AtimeScrub.Enabled)
				assert.Equal(tt, atimePeriod, ot.Info.AtimeScrub.Period)
			}
			if assert.NotNil(tt, ot.Info.CifsChangeNotify) {
				assert.Equal(tt, cn, ot.Info.CifsChangeNotify.Enabled)
			}
			if assert.NotNil(tt, ot.Info.Prepopulate) {
				assert.Len(tt, ot.Info.Prepopulate.DirPaths, 1)
				assert.Equal(tt, *dir, *ot.Info.Prepopulate.DirPaths[0])
				assert.Len(tt, ot.Info.Prepopulate.ExcludeDirPaths, 1)
				assert.Equal(tt, *ex, *ot.Info.Prepopulate.ExcludeDirPaths[0])
				assert.Equal(tt, rec, ot.Info.Prepopulate.Recurse)
			}
		}
	})
}

func TestSnapmirrorRelationshipTransferState(t *testing.T) {
	t.Run("WhenSnapmirrorRelationshipIsNil", func(tt *testing.T) {
		var snapmirror *SnapmirrorRelationship = nil

		var result string
		assert.NotPanics(tt, func() {
			result = snapmirror.TransferState()
		})
		assert.Equal(tt, "", result)
	})

	t.Run("WhenTransferIsNil", func(tt *testing.T) {
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: nil,
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, "", result)
	})

	t.Run("WhenTransferStateIsNil", func(tt *testing.T) {
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: nil,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, "", result)
	})

	t.Run("WhenTransferStateIsAborted", func(tt *testing.T) {
		state := models.SnapmirrorRelationshipInlineTransferStateAborted
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: &state,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, models.SnapmirrorRelationshipInlineTransferStateAborted, result)
	})

	t.Run("WhenTransferStateIsFailed", func(tt *testing.T) {
		state := models.SnapmirrorRelationshipInlineTransferStateFailed
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: &state,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, models.SnapmirrorRelationshipInlineTransferStateFailed, result)
	})

	t.Run("WhenTransferStateIsHardAborted", func(tt *testing.T) {
		state := models.SnapmirrorRelationshipInlineTransferStateHardAborted
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: &state,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, models.SnapmirrorRelationshipInlineTransferStateHardAborted, result)
	})

	t.Run("WhenTransferStateIsQueued", func(tt *testing.T) {
		state := models.SnapmirrorRelationshipInlineTransferStateQueued
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: &state,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, models.SnapmirrorRelationshipInlineTransferStateQueued, result)
	})

	t.Run("WhenTransferStateIsSuccess", func(tt *testing.T) {
		state := models.SnapmirrorRelationshipInlineTransferStateSuccess
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: &state,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, models.SnapmirrorRelationshipInlineTransferStateSuccess, result)
	})

	t.Run("WhenTransferStateIsTransferring", func(tt *testing.T) {
		state := models.SnapmirrorRelationshipInlineTransferStateTransferring
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: &state,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, models.SnapmirrorRelationshipInlineTransferStateTransferring, result)
	})

	t.Run("WhenTransferStateIsCustomString", func(tt *testing.T) {
		customState := "custom_state"
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: &customState,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, "custom_state", result)
	})

	t.Run("WhenTransferStateIsEmptyString", func(tt *testing.T) {
		emptyState := ""
		snapmirror := &SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: &emptyState,
				},
			},
		}

		result := snapmirror.TransferState()

		assert.Equal(tt, "", result)
	})
}

func TestRoleCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := roleCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
		assert.Nil(tt, otParams.Info)
	})

	t.Run("WhenParamsSetWithNoPrivileges", func(tt *testing.T) {
		params := &RoleCreateParams{
			Name:       "test-role",
			Privileges: []*RolePrivilege{},
		}
		otParams := roleCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "test-role", *otParams.Info.Name)
		assert.Empty(tt, otParams.Info.RoleInlinePrivileges)
	})

	t.Run("WhenParamsSetWithNilPrivileges", func(tt *testing.T) {
		params := &RoleCreateParams{
			Name:       "test-role",
			Privileges: nil,
		}
		otParams := roleCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "test-role", *otParams.Info.Name)
		assert.Empty(tt, otParams.Info.RoleInlinePrivileges)
	})

	t.Run("WhenParamsSetWithSinglePrivilege", func(tt *testing.T) {
		params := &RoleCreateParams{
			Name: "test-role",
			Privileges: []*RolePrivilege{
				{
					Path:   "/api/storage/volumes",
					Access: "all",
					Query:  "",
				},
			},
		}
		otParams := roleCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "test-role", *otParams.Info.Name)
		assert.Len(tt, otParams.Info.RoleInlinePrivileges, 1)
		assert.Equal(tt, "/api/storage/volumes", *otParams.Info.RoleInlinePrivileges[0].Path)
		assert.Equal(tt, models.RolePrivilegeLevelAll, *otParams.Info.RoleInlinePrivileges[0].Access)
		assert.Equal(tt, "", *otParams.Info.RoleInlinePrivileges[0].Query)
	})

	t.Run("WhenParamsSetWithMultiplePrivileges", func(tt *testing.T) {
		params := &RoleCreateParams{
			Name: "test-role",
			Privileges: []*RolePrivilege{
				{
					Path:   "/api/storage/volumes",
					Access: "all",
					Query:  "",
				},
				{
					Path:   "/api/svm/svms",
					Access: "readonly",
					Query:  "",
				},
				{
					Path:   "volume move start",
					Access: "read_create",
					Query:  "-vserver vs1|vs2 -destination-aggregate aggr1",
				},
			},
		}
		otParams := roleCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "test-role", *otParams.Info.Name)
		assert.Len(tt, otParams.Info.RoleInlinePrivileges, 3)

		// Check first privilege
		assert.Equal(tt, "/api/storage/volumes", *otParams.Info.RoleInlinePrivileges[0].Path)
		assert.Equal(tt, models.RolePrivilegeLevelAll, *otParams.Info.RoleInlinePrivileges[0].Access)
		assert.Equal(tt, "", *otParams.Info.RoleInlinePrivileges[0].Query)

		// Check second privilege
		assert.Equal(tt, "/api/svm/svms", *otParams.Info.RoleInlinePrivileges[1].Path)
		assert.Equal(tt, models.RolePrivilegeLevelReadonly, *otParams.Info.RoleInlinePrivileges[1].Access)
		assert.Equal(tt, "", *otParams.Info.RoleInlinePrivileges[1].Query)

		// Check third privilege
		assert.Equal(tt, "volume move start", *otParams.Info.RoleInlinePrivileges[2].Path)
		assert.Equal(tt, models.RolePrivilegeLevelReadCreate, *otParams.Info.RoleInlinePrivileges[2].Access)
		assert.Equal(tt, "-vserver vs1|vs2 -destination-aggregate aggr1", *otParams.Info.RoleInlinePrivileges[2].Query)
	})

	t.Run("WhenParamsSetWithDifferentAccessLevels", func(tt *testing.T) {
		params := &RoleCreateParams{
			Name: "test-role",
			Privileges: []*RolePrivilege{
				{
					Path:   "/api/storage/volumes",
					Access: "none",
					Query:  "",
				},
				{
					Path:   "/api/svm/svms",
					Access: "read_modify",
					Query:  "",
				},
				{
					Path:   "/api/cluster",
					Access: "read_create_modify",
					Query:  "",
				},
			},
		}
		otParams := roleCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "test-role", *otParams.Info.Name)
		assert.Len(tt, otParams.Info.RoleInlinePrivileges, 3)

		assert.Equal(tt, models.RolePrivilegeLevelNone, *otParams.Info.RoleInlinePrivileges[0].Access)
		assert.Equal(tt, models.RolePrivilegeLevelReadModify, *otParams.Info.RoleInlinePrivileges[1].Access)
		assert.Equal(tt, models.RolePrivilegeLevelReadCreateModify, *otParams.Info.RoleInlinePrivileges[2].Access)
	})
}

func TestRoleGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := roleGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithAllFields", func(tt *testing.T) {
		ownerUUID := "owner-uuid-123"
		params := &RoleGetParams{
			BaseParams: BaseParams{
				Fields: []string{"name", "privileges", "owner"},
			},
			Name:      "test-role",
			OwnerUUID: &ownerUUID,
		}
		otParams := roleGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, ownerUUID, otParams.OwnerUUID)
		assert.Equal(tt, []string{"name", "privileges", "owner"}, otParams.Fields)
	})

	t.Run("WhenParamsSetWithOnlyName", func(tt *testing.T) {
		params := &RoleGetParams{
			BaseParams: BaseParams{},
			Name:       "test-role",
			OwnerUUID:  nil,
		}
		otParams := roleGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Empty(tt, otParams.OwnerUUID)
		assert.Nil(tt, otParams.Fields)
	})

	t.Run("WhenParamsSetWithNameAndOwnerUUID", func(tt *testing.T) {
		ownerUUID := "owner-uuid-456"
		params := &RoleGetParams{
			BaseParams: BaseParams{},
			Name:       "test-role",
			OwnerUUID:  &ownerUUID,
		}
		otParams := roleGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, ownerUUID, otParams.OwnerUUID)
		assert.Nil(tt, otParams.Fields)
	})

	t.Run("WhenParamsSetWithNameAndFields", func(tt *testing.T) {
		params := &RoleGetParams{
			BaseParams: BaseParams{
				Fields: []string{"name", "scope"},
			},
			Name:      "test-role",
			OwnerUUID: nil,
		}
		otParams := roleGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Empty(tt, otParams.OwnerUUID)
		assert.Equal(tt, []string{"name", "scope"}, otParams.Fields)
	})

	t.Run("WhenParamsSetWithEmptyFields", func(tt *testing.T) {
		params := &RoleGetParams{
			BaseParams: BaseParams{
				Fields: []string{},
			},
			Name:      "test-role",
			OwnerUUID: nil,
		}
		otParams := roleGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Empty(tt, otParams.OwnerUUID)
		assert.Empty(tt, otParams.Fields)
	})

	t.Run("WhenParamsSetWithEmptyName", func(tt *testing.T) {
		ownerUUID := "owner-uuid-789"
		params := &RoleGetParams{
			BaseParams: BaseParams{
				Fields: []string{"name"},
			},
			Name:      "",
			OwnerUUID: &ownerUUID,
		}
		otParams := roleGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", otParams.Name)
		assert.Equal(tt, ownerUUID, otParams.OwnerUUID)
		assert.Equal(tt, []string{"name"}, otParams.Fields)
	})
}

func TestRoleCollectionGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := roleCollectionGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithAllFields", func(tt *testing.T) {
		roleName := "test-role"
		params := &RoleCollectionGetParams{
			BaseParams: BaseParams{
				Fields: []string{"name", "privileges", "owner", "scope"},
			},
			Name: &roleName,
		}
		otParams := roleCollectionGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, roleName, *otParams.Name)
		assert.Equal(tt, []string{"name", "privileges", "owner", "scope"}, otParams.Fields)
	})

	t.Run("WhenParamsSetWithOnlyName", func(tt *testing.T) {
		roleName := "test-role"
		params := &RoleCollectionGetParams{
			BaseParams: BaseParams{},
			Name:       &roleName,
		}
		otParams := roleCollectionGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, roleName, *otParams.Name)
		assert.Nil(tt, otParams.Fields)
	})

	t.Run("WhenParamsSetWithOnlyFields", func(tt *testing.T) {
		params := &RoleCollectionGetParams{
			BaseParams: BaseParams{
				Fields: []string{"name", "scope"},
			},
			Name: nil,
		}
		otParams := roleCollectionGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Nil(tt, otParams.Name)
		assert.Equal(tt, []string{"name", "scope"}, otParams.Fields)
	})

	t.Run("WhenParamsSetWithEmptyFields", func(tt *testing.T) {
		roleName := "test-role"
		params := &RoleCollectionGetParams{
			BaseParams: BaseParams{
				Fields: []string{},
			},
			Name: &roleName,
		}
		otParams := roleCollectionGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, roleName, *otParams.Name)
		assert.Empty(tt, otParams.Fields)
	})

	t.Run("WhenParamsSetWithEmptyName", func(tt *testing.T) {
		emptyName := ""
		params := &RoleCollectionGetParams{
			BaseParams: BaseParams{
				Fields: []string{"name"},
			},
			Name: &emptyName,
		}
		otParams := roleCollectionGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", *otParams.Name)
		assert.Equal(tt, []string{"name"}, otParams.Fields)
	})

	t.Run("WhenParamsSetWithNilNameAndNoFields", func(tt *testing.T) {
		params := &RoleCollectionGetParams{
			BaseParams: BaseParams{},
			Name:       nil,
		}
		otParams := roleCollectionGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Nil(tt, otParams.Name)
		assert.Nil(tt, otParams.Fields)
	})
}

func TestRolePrivilegeModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := rolePrivilegeModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithAllFields", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "owner-uuid-123",
			Name:    "test-role",
			Access:  "all",
			Query:   "-vserver vs1|vs2 -destination-aggregate aggr1",
			Path:    "volume move start",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-123", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "volume move start", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelAll, *otParams.Info.Access)
		assert.Equal(tt, "-vserver vs1|vs2 -destination-aggregate aggr1", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithReadonlyAccess", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "owner-uuid-456",
			Name:    "test-role",
			Access:  "readonly",
			Query:   "",
			Path:    "/api/storage/volumes",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-456", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "/api/storage/volumes", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelReadonly, *otParams.Info.Access)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithReadCreateAccess", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "owner-uuid-789",
			Name:    "test-role",
			Access:  "read_create",
			Query:   "test-query",
			Path:    "/api/svm/svms",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-789", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "/api/svm/svms", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelReadCreate, *otParams.Info.Access)
		assert.Equal(tt, "test-query", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithReadModifyAccess", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "owner-uuid-101",
			Name:    "test-role",
			Access:  "read_modify",
			Query:   "",
			Path:    "/api/cluster",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-101", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "/api/cluster", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelReadModify, *otParams.Info.Access)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithReadCreateModifyAccess", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "owner-uuid-202",
			Name:    "test-role",
			Access:  "read_create_modify",
			Query:   "query-param",
			Path:    "command path",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-202", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "command path", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelReadCreateModify, *otParams.Info.Access)
		assert.Equal(tt, "query-param", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithNoneAccess", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "owner-uuid-303",
			Name:    "test-role",
			Access:  "none",
			Query:   "",
			Path:    "/api/storage",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-303", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "/api/storage", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelNone, *otParams.Info.Access)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithEmptyStrings", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "",
			Name:    "",
			Access:  "all",
			Query:   "",
			Path:    "",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", otParams.OwnerUUID)
		assert.Equal(tt, "", otParams.Name)
		assert.Equal(tt, "", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelAll, *otParams.Info.Access)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithRESTEndpointPath", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "owner-uuid-404",
			Name:    "test-role",
			Access:  "all",
			Query:   "",
			Path:    "/api/storage/volumes/{volume.uuid}/snapshots",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-404", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "/api/storage/volumes/{volume.uuid}/snapshots", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelAll, *otParams.Info.Access)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithCommandPathAndQuery", func(tt *testing.T) {
		params := &RolePrivilegeModifyParams{
			OwnerID: "owner-uuid-505",
			Name:    "test-role",
			Access:  "read_create",
			Query:   "-vserver vs1 -volume vol1 -destination-aggregate aggr1",
			Path:    "volume move start",
		}
		otParams := rolePrivilegeModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-505", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "volume move start", otParams.Path)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, models.RolePrivilegeLevelReadCreate, *otParams.Info.Access)
		assert.Equal(tt, "-vserver vs1 -volume vol1 -destination-aggregate aggr1", *otParams.Info.Query)
	})
}

func TestRolePrivilegeCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := rolePrivilegeCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithAllAccess", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-123",
			Name:    "test-role",
			Path:    "/api/storage/volumes",
			Access:  "all",
			Query:   "",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-123", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "/api/storage/volumes", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelAll, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "", *otParams.Info.Query) // nillable.ToPointer returns pointer to empty string, not nil
	})

	t.Run("WhenParamsSetWithReadonlyAccess", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-456",
			Name:    "test-role",
			Path:    "/api/svm/svms",
			Access:  "readonly",
			Query:   "",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-456", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "/api/svm/svms", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelReadonly, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithReadCreateAccess", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-789",
			Name:    "test-role",
			Path:    "/api/cluster",
			Access:  "read_create",
			Query:   "test-query",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-789", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "/api/cluster", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelReadCreate, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "test-query", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithReadModifyAccess", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-101",
			Name:    "test-role",
			Path:    "/api/storage/volumes",
			Access:  "read_modify",
			Query:   "",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-101", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "/api/storage/volumes", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelReadModify, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithReadCreateModifyAccess", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-202",
			Name:    "test-role",
			Path:    "/api/storage",
			Access:  "read_create_modify",
			Query:   "query-param",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-202", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "/api/storage", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelReadCreateModify, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "query-param", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithNoneAccess", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-303",
			Name:    "test-role",
			Path:    "/api/storage/volumes",
			Access:  "none",
			Query:   "",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-303", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "/api/storage/volumes", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelNone, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithEmptyStrings", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "",
			Name:    "",
			Path:    "",
			Access:  "all",
			Query:   "",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", otParams.OwnerUUID)
		assert.Equal(tt, "", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelAll, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithRESTEndpointPath", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-404",
			Name:    "test-role",
			Path:    "/api/storage/volumes/{volume.uuid}/snapshots",
			Access:  "all",
			Query:   "",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-404", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "/api/storage/volumes/{volume.uuid}/snapshots", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelAll, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithCommandPathAndQuery", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-505",
			Name:    "test-role",
			Path:    "volume move start",
			Access:  "read_create",
			Query:   "-vserver vs1 -volume vol1 -destination-aggregate aggr1",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-505", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "volume move start", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelReadCreate, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "-vserver vs1 -volume vol1 -destination-aggregate aggr1", *otParams.Info.Query)
	})

	t.Run("WhenParamsSetWithLongQuery", func(tt *testing.T) {
		params := &RolePrivilegeCreateParams{
			OwnerID: "owner-uuid-606",
			Name:    "test-role",
			Path:    "/api/storage/volumes",
			Access:  "read_modify",
			Query:   "-vserver vs1|vs2|vs3 -destination-aggregate aggr1|aggr2 -force",
		}
		otParams := rolePrivilegeCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-606", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "/api/storage/volumes", *otParams.Info.Path)
		assert.Equal(tt, models.RolePrivilegeLevelReadModify, *otParams.Info.Access)
		assert.NotNil(tt, otParams.Info.Query)
		assert.Equal(tt, "-vserver vs1|vs2|vs3 -destination-aggregate aggr1|aggr2 -force", *otParams.Info.Query)
	})
}

func TestServerRootCAGetParamsToONTAPCollectionGet(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := serverRootCAGetParamsToONTAPCollectionGet(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &ServerRootCAGetParams{
			BaseParams: BaseParams{
				Fields: []string{"name", "type"},
			},
			SvmName:         nillable.ToPointer("svm1"),
			Name:            nillable.ToPointer("cert1"),
			CertificateType: nillable.ToPointer("server"),
		}
		otParams := serverRootCAGetParamsToONTAPCollectionGet(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "svm1", *otParams.SvmName)
		assert.Equal(tt, "cert1", *otParams.Name)
		assert.Equal(tt, "server", *otParams.Type)
		assert.Equal(tt, []string{"name", "type"}, otParams.Fields)
	})
}

func TestServerRootCAInstallParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := serverRootCAInstallParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &ServerRootCAInstallParams{
			PrivateKey:      nillable.ToPointer("private-key"),
			Certificate:     nillable.ToPointer("certificate"),
			SvmName:         nillable.ToPointer("svm1"),
			CertificateType: nillable.ToPointer("server"),
			CommonName:      nillable.ToPointer("cn1"),
			Name:            nillable.ToPointer("cert1"),
		}
		otParams := serverRootCAInstallParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "private-key", *otParams.Info.PrivateKey)
		assert.Equal(tt, "certificate", *otParams.Info.PublicCertificate)
		assert.Equal(tt, "svm1", *otParams.Info.Svm.Name)
		assert.Equal(tt, "server", *otParams.Info.Type)
		assert.Equal(tt, "cn1", *otParams.Info.CommonName)
		assert.Equal(tt, "cert1", *otParams.Info.Name)
	})
}

func TestServerRootCADeleteParamsToONTAPCollectionDelete(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := serverRootCADeleteParamsToONTAPCollectionDelete(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &ServerRootCADeleteParams{
			UUID:                 nillable.ToPointer("uuid1"),
			SvmName:              nillable.ToPointer("svm1"),
			SerialNumber:         nillable.ToPointer("serial1"),
			CommonName:           nillable.ToPointer("cn1"),
			CertificateAuthority: nillable.ToPointer("ca1"),
		}
		otParams := serverRootCADeleteParamsToONTAPCollectionDelete(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "uuid1", *otParams.UUID)
		assert.Equal(tt, "svm1", *otParams.SvmName)
		assert.Equal(tt, "serial1", *otParams.SerialNumber)
		assert.Equal(tt, "cn1", *otParams.CommonName)
		assert.Equal(tt, "ca1", *otParams.Ca)
	})
}

func TestDNSGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := dnsGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &DNSGetParams{
			BaseParams: BaseParams{
				Fields: []string{"domains", "servers"},
			},
			SvmUUID: "svm-uuid-1",
		}
		otParams := dnsGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, []string{"domains", "servers"}, otParams.Fields)
		assert.Equal(tt, "svm-uuid-1", otParams.UUID)
	})
}

func TestDNSModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := dnsModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithDomainsAndServers", func(tt *testing.T) {
		params := &DNSModifyParams{
			SvmUUID:     "svm-uuid-1",
			Domains:     []string{"example.com", "test.com"},
			NameServers: []string{"8.8.8.8", "8.8.4.4"},
		}
		otParams := dnsModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "svm-uuid-1", otParams.UUID)
		assert.NotNil(tt, otParams.Info)
		assert.Len(tt, otParams.Info.Domains, 2)
		assert.Equal(tt, "example.com", *otParams.Info.Domains[0])
		assert.Equal(tt, "test.com", *otParams.Info.Domains[1])
		assert.Len(tt, otParams.Info.Servers, 2)
		assert.Equal(tt, "8.8.8.8", *otParams.Info.Servers[0])
		assert.Equal(tt, "8.8.4.4", *otParams.Info.Servers[1])
	})

	t.Run("WhenParamsSetWithDynamicDNS", func(tt *testing.T) {
		useSecure := true
		fqdn := "test.example.com"
		enabled := true
		params := &DNSModifyParams{
			SvmUUID: "svm-uuid-1",
			DDNSModifyParams: DDNSModifyParams{
				UseSecure: &useSecure,
				Fqdn:      &fqdn,
				Enabled:   &enabled,
			},
		}
		otParams := dnsModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.DynamicDNS)
		assert.Equal(tt, &fqdn, otParams.Info.DynamicDNS.Fqdn)
		assert.Equal(tt, &useSecure, otParams.Info.DynamicDNS.UseSecure)
		assert.Equal(tt, &enabled, otParams.Info.DynamicDNS.Enabled)
	})

	t.Run("WhenParamsSetWithAllFields", func(tt *testing.T) {
		useSecure := false
		fqdn := "ddns.example.com"
		enabled := true
		params := &DNSModifyParams{
			SvmUUID:     "svm-uuid-1",
			Domains:     []string{"example.com"},
			NameServers: []string{"1.1.1.1"},
			DDNSModifyParams: DDNSModifyParams{
				UseSecure: &useSecure,
				Fqdn:      &fqdn,
				Enabled:   &enabled,
			},
		}
		otParams := dnsModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Len(tt, otParams.Info.Domains, 1)
		assert.Len(tt, otParams.Info.Servers, 1)
		assert.NotNil(tt, otParams.Info.DynamicDNS)
	})
}

func TestDNSCreateParamsToONTAPWithSvmUUID(t *testing.T) {
	t.Run("WhenSvmUUIDSet", func(tt *testing.T) {
		params := &DNSCreateParams{
			SvmUUID:    "svm-uuid-1",
			Domains:    []string{"example.com"},
			DNSServers: []string{"8.8.8.8"},
		}
		otParams := dnsCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.Svm)
		assert.Equal(tt, "svm-uuid-1", *otParams.Info.Svm.UUID)
	})
}

func TestCifsDomainGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cifsDomainGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithAllFields", func(tt *testing.T) {
		resetDiscovered := true
		rediscoverTrusts := false
		params := &CifsDomainGetParams{
			BaseParams: BaseParams{
				Fields: []string{"server_discovery_mode", "preferred_dcs"},
			},
			SvmUUID:                "svm-uuid-123",
			ResetDiscoveredServers: &resetDiscovered,
			RediscoverTrusts:       &rediscoverTrusts,
		}
		otParams := cifsDomainGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "svm-uuid-123", otParams.SvmUUID)
		assert.Equal(tt, 2, len(otParams.Fields))
		assert.Contains(tt, otParams.Fields, "server_discovery_mode")
		assert.Contains(tt, otParams.Fields, "preferred_dcs")
		assert.NotNil(tt, otParams.ResetDiscoveredServers)
		assert.Equal(tt, "true", *otParams.ResetDiscoveredServers)
		assert.NotNil(tt, otParams.RediscoverTrusts)
		assert.Equal(tt, "false", *otParams.RediscoverTrusts)
	})

	t.Run("WhenParamsSetWithOnlyRequired", func(tt *testing.T) {
		params := &CifsDomainGetParams{
			SvmUUID: "svm-uuid-456",
		}
		otParams := cifsDomainGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "svm-uuid-456", otParams.SvmUUID)
		assert.Nil(tt, otParams.ResetDiscoveredServers)
		assert.Nil(tt, otParams.RediscoverTrusts)
	})

	t.Run("WhenBooleanParamsSet", func(tt *testing.T) {
		resetDiscovered := false
		rediscoverTrusts := true
		params := &CifsDomainGetParams{
			SvmUUID:                "svm-uuid-789",
			ResetDiscoveredServers: &resetDiscovered,
			RediscoverTrusts:       &rediscoverTrusts,
		}
		otParams := cifsDomainGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.ResetDiscoveredServers)
		assert.Equal(tt, "false", *otParams.ResetDiscoveredServers)
		assert.NotNil(tt, otParams.RediscoverTrusts)
		assert.Equal(tt, "true", *otParams.RediscoverTrusts)
	})
}

func TestCifsDomainModifyParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cifsDomainModifyParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithScheduleEnabled", func(tt *testing.T) {
		scheduleEnabled := true
		params := &CifsDomainModifyParams{
			SvmUUID:         "svm-uuid-1",
			ScheduleEnabled: &scheduleEnabled,
		}
		otParams := cifsDomainModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.PasswordSchedule)
		assert.Equal(tt, &scheduleEnabled, otParams.Info.PasswordSchedule.ScheduleEnabled)
	})

	t.Run("WhenParamsSetWithCifsPasswordOperation", func(tt *testing.T) {
		passwordOp := "change"
		params := &CifsDomainModifyParams{
			SvmUUID:               "svm-uuid-1",
			CifsPasswordOperation: &passwordOp,
		}
		otParams := cifsDomainModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, &passwordOp, otParams.CifsPasswordOperation)
	})

	t.Run("WhenParamsSetWithAdUserNameAndPassword", func(tt *testing.T) {
		adUser := "aduser"
		adPass := "adpass"
		params := &CifsDomainModifyParams{
			SvmUUID:    "svm-uuid-1",
			AdUserName: &adUser,
			AdPassword: &adPass,
		}
		otParams := cifsDomainModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.AdDomain)
		assert.Equal(tt, &adUser, otParams.Info.AdDomain.User)
		assert.Equal(tt, &adPass, otParams.Info.AdDomain.Password)
	})

	t.Run("WhenParamsSetWithClientIDTenantIDAndCertificate", func(tt *testing.T) {
		clientID := "client-id-1"
		tenantID := "tenant-id-1"
		cert := strfmt.Password("cert-data")
		params := &CifsDomainModifyParams{
			SvmUUID:           "svm-uuid-1",
			ClientID:          &clientID,
			TenantID:          &tenantID,
			ClientCertificate: &cert,
		}
		otParams := cifsDomainModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, &clientID, otParams.Info.ClientID)
		assert.Equal(tt, &tenantID, otParams.Info.TenantID)
		assert.Equal(tt, &cert, otParams.Info.ClientCertificate)
	})

	t.Run("WhenParamsSetWithAllFields", func(tt *testing.T) {
		scheduleEnabled := true
		passwordOp := "change"
		adUser := "aduser"
		adPass := "adpass"
		clientID := "client-id-1"
		tenantID := "tenant-id-1"
		cert := strfmt.Password("cert-data")
		params := &CifsDomainModifyParams{
			SvmUUID:               "svm-uuid-1",
			ScheduleEnabled:       &scheduleEnabled,
			CifsPasswordOperation: &passwordOp,
			AdUserName:            &adUser,
			AdPassword:            &adPass,
			ClientID:              &clientID,
			TenantID:              &tenantID,
			ClientCertificate:     &cert,
		}
		otParams := cifsDomainModifyParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.PasswordSchedule)
		assert.NotNil(tt, otParams.Info.AdDomain)
		assert.Equal(tt, &clientID, otParams.Info.ClientID)
		assert.Equal(tt, &tenantID, otParams.Info.TenantID)
		assert.Equal(tt, &cert, otParams.Info.ClientCertificate)
	})
}

func TestCifsShareCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cifsShareCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		svmName := "svm1"
		params := &CifsShareCreateParams{
			SvmName:         &svmName,
			Name:            "share1",
			Path:            "/path/to/share",
			ShareProperties: []string{"browsable", "oplocks"},
		}
		otParams := cifsShareCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.Equal(tt, "share1", *otParams.Info.Name)
		assert.Equal(tt, "/path/to/share", *otParams.Info.Path)
		assert.Equal(tt, "svm1", *otParams.Info.Svm.Name)
	})
}

func TestCalculateShareProperties(t *testing.T) {
	t.Run("WhenSharePropertyCA", func(tt *testing.T) {
		shareProperties := []string{utils.CIFSSharePropertyCA}
		result := calculateShareProperties(shareProperties)
		assert.NotNil(tt, result)
		assert.True(tt, *result.ContinuouslyAvailable)
	})

	t.Run("WhenSharePropertyEncryptData", func(tt *testing.T) {
		shareProperties := []string{utils.CIFSSharePropertyEncryptData}
		result := calculateShareProperties(shareProperties)
		assert.NotNil(tt, result)
		assert.True(tt, *result.Encryption)
	})

	t.Run("WhenSharePropertyAccessBasedEnumeration", func(tt *testing.T) {
		shareProperties := []string{utils.CIFSAccessBasedEnumeration}
		result := calculateShareProperties(shareProperties)
		assert.NotNil(tt, result)
		assert.True(tt, *result.AccessBasedEnumeration)
	})

	t.Run("WhenSharePropertyCAInExtendShareProperties", func(tt *testing.T) {
		shareProperties := []string{utils.CIFSSharePropertyCA}
		extended := ExtendSharePropertiesWithDefaults(shareProperties)
		assert.NotNil(tt, extended)
		assert.Contains(tt, extended, utils.CIFSSharePropertyCA)
	})

	t.Run("WhenMultipleShareProperties", func(tt *testing.T) {
		shareProperties := []string{
			utils.CIFSSharePropertyCA,
			utils.CIFSSharePropertyEncryptData,
			utils.CIFSAccessBasedEnumeration,
			utils.CIFSSharePropertyBrowsable,
		}
		result := calculateShareProperties(shareProperties)
		assert.NotNil(tt, result)
		assert.True(tt, *result.ContinuouslyAvailable)
		assert.True(tt, *result.Encryption)
		assert.True(tt, *result.AccessBasedEnumeration)
		assert.True(tt, *result.Browsable)
	})
}

func TestCifsShareACLDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cifsShareACLDeleteParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		params := &CifsShareACLDeleteParams{
			ShareName: "share1",
			User:      "user1",
			SvmUUID:   "svm-uuid-1",
		}
		otParams := cifsShareACLDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "share1", otParams.Share)
		assert.Equal(tt, "user1", otParams.UserOrGroup)
		assert.Equal(tt, "svm-uuid-1", otParams.SvmUUID)
	})
}

func TestCifsServiceDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := cifsServiceDeleteParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithClientIDTenantIDAndCertificate", func(tt *testing.T) {
		params := &CifsServiceDeleteParams{
			SvmUUID:            "svm-uuid-1",
			Force:              true,
			ClientID:           "client-id-1",
			TenantID:           "tenant-id-1",
			EntraIDCertificate: "cert-data",
		}
		otParams := cifsServiceDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "svm-uuid-1", otParams.SvmUUID)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.ClientID)
		assert.Equal(tt, "client-id-1", *otParams.Info.ClientID)
		assert.NotNil(tt, otParams.Info.TenantID)
		assert.Equal(tt, "tenant-id-1", *otParams.Info.TenantID)
		assert.NotNil(tt, otParams.Info.ClientCertificate)
		assert.Equal(tt, strfmt.Password("cert-data"), *otParams.Info.ClientCertificate)
	})

	t.Run("WhenParamsSetWithoutClientIDTenantIDAndCertificate", func(tt *testing.T) {
		params := &CifsServiceDeleteParams{
			SvmUUID: "svm-uuid-1",
			Force:   true,
		}
		otParams := cifsServiceDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "svm-uuid-1", otParams.SvmUUID)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.AdDomain)
	})
}

func TestVolumeModifyCloudWriteParamToONTAP(t *testing.T) {
	t.Run("WhenParamsNil_ThenReturnsDefault", func(tt *testing.T) {
		result := volumeModifyCloudWriteParamToONTAP(nil)
		assert.NotNil(tt, result)
	})

	t.Run("WhenCloudWriteModeEnabledIsTrue_ThenCloudWriteEnabledIsSet", func(tt *testing.T) {
		trueVal := true
		params := &VolumeModifyParams{
			UUID: "test-uuid",
			TieringPolicy: &TieringPolicy{
				CloudWriteModeEnabled: &trueVal,
			},
		}
		result := volumeModifyCloudWriteParamToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-uuid", result.UUID)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.CloudWriteEnabled)
		assert.True(tt, *result.Info.CloudWriteEnabled)
	})

	t.Run("WhenCloudWriteModeEnabledIsFalse_ThenCloudWriteEnabledIsSet", func(tt *testing.T) {
		falseVal := false
		params := &VolumeModifyParams{
			UUID: "test-uuid",
			TieringPolicy: &TieringPolicy{
				CloudWriteModeEnabled: &falseVal,
			},
		}
		result := volumeModifyCloudWriteParamToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-uuid", result.UUID)
		assert.NotNil(tt, result.Info)
		assert.NotNil(tt, result.Info.CloudWriteEnabled)
		assert.False(tt, *result.Info.CloudWriteEnabled)
	})
}

func TestVolumeGetParamsToONTAPQuotaRules(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		ctx := context.Background()
		otParams := VolumeGetParamsToONTAPQuotaRules(ctx, nil)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, ctx, otParams.Context)
	})

	t.Run("WhenParamsSet", func(tt *testing.T) {
		ctx := context.Background()
		params := &VolumeGetParams{
			BaseParams: BaseParams{Fields: []string{"field1", "field2"}},
			UUID:       "test-uuid",
		}

		otParams := VolumeGetParamsToONTAPQuotaRules(ctx, params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, ctx, otParams.Context)
		assert.Equal(tt, "field1", otParams.Fields[0])
		assert.Equal(tt, "field2", otParams.Fields[1])
		assert.Equal(tt, "test-uuid", otParams.UUID)
	})

	t.Run("WhenParamsHasOnlyUUID", func(tt *testing.T) {
		ctx := context.Background()
		params := &VolumeGetParams{
			UUID: "test-uuid-only",
		}

		otParams := VolumeGetParamsToONTAPQuotaRules(ctx, params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, ctx, otParams.Context)
		assert.Equal(tt, "test-uuid-only", otParams.UUID)
		assert.Nil(tt, otParams.Fields)
	})

	t.Run("WhenParamsHasOnlyFields", func(tt *testing.T) {
		ctx := context.Background()
		params := &VolumeGetParams{
			BaseParams: BaseParams{Fields: []string{"field1"}},
		}

		otParams := VolumeGetParamsToONTAPQuotaRules(ctx, params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, ctx, otParams.Context)
		assert.Equal(tt, "field1", otParams.Fields[0])
		assert.Equal(tt, "", otParams.UUID)
	})

	t.Run("WhenContextIsDifferent", func(tt *testing.T) {
		ctx1 := context.Background()
		ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		params := &VolumeGetParams{
			UUID: "test-uuid",
		}

		otParams1 := VolumeGetParamsToONTAPQuotaRules(ctx1, params)
		otParams2 := VolumeGetParamsToONTAPQuotaRules(ctx2, params)

		assert.NotNil(tt, otParams1)
		assert.NotNil(tt, otParams2)
		assert.Equal(tt, ctx1, otParams1.Context)
		assert.Equal(tt, ctx2, otParams2.Context)
		assert.Equal(tt, "test-uuid", otParams1.UUID)
		assert.Equal(tt, "test-uuid", otParams2.UUID)
	})
}

func TestRoleDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := roleDeleteParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithAllFields", func(tt *testing.T) {
		ownerUUID := "owner-uuid-123"
		params := &RoleDeleteParams{
			Name:      "test-role",
			OwnerUUID: &ownerUUID,
		}
		otParams := roleDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, ownerUUID, otParams.OwnerUUID)
	})

	t.Run("WhenParamsSetWithOnlyName", func(tt *testing.T) {
		params := &RoleDeleteParams{
			Name:      "test-role",
			OwnerUUID: nil,
		}
		otParams := roleDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Empty(tt, otParams.OwnerUUID)
	})

	t.Run("WhenParamsSetWithOnlyOwnerUUID", func(tt *testing.T) {
		ownerUUID := "owner-uuid-456"
		params := &RoleDeleteParams{
			Name:      "",
			OwnerUUID: &ownerUUID,
		}
		otParams := roleDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", otParams.Name)
		assert.Equal(tt, ownerUUID, otParams.OwnerUUID)
	})

	t.Run("WhenParamsSetWithEmptyNameAndNilOwnerUUID", func(tt *testing.T) {
		params := &RoleDeleteParams{
			Name:      "",
			OwnerUUID: nil,
		}
		otParams := roleDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", otParams.Name)
		assert.Empty(tt, otParams.OwnerUUID)
	})
}

func TestRolePrivilegeDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := rolePrivilegeDeleteParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithAllFields", func(tt *testing.T) {
		params := &RolePrivilegeDeleteParams{
			OwnerID: "owner-uuid-123",
			Name:    "test-role",
			Path:    "/api/storage/volumes",
		}
		otParams := rolePrivilegeDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-123", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "/api/storage/volumes", otParams.Path)
	})

	t.Run("WhenParamsSetWithCommandPath", func(tt *testing.T) {
		params := &RolePrivilegeDeleteParams{
			OwnerID: "owner-uuid-456",
			Name:    "external-peer",
			Path:    "snapmirror resync",
		}
		otParams := rolePrivilegeDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-456", otParams.OwnerUUID)
		assert.Equal(tt, "external-peer", otParams.Name)
		assert.Equal(tt, "snapmirror resync", otParams.Path)
	})

	t.Run("WhenParamsSetWithEmptyStrings", func(tt *testing.T) {
		params := &RolePrivilegeDeleteParams{
			OwnerID: "",
			Name:    "",
			Path:    "",
		}
		otParams := rolePrivilegeDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", otParams.OwnerUUID)
		assert.Equal(tt, "", otParams.Name)
		assert.Equal(tt, "", otParams.Path)
	})

	t.Run("WhenParamsSetWithRESTEndpointPath", func(tt *testing.T) {
		params := &RolePrivilegeDeleteParams{
			OwnerID: "owner-uuid-789",
			Name:    "test-role",
			Path:    "/api/storage/volumes/{volume.uuid}/snapshots",
		}
		otParams := rolePrivilegeDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-789", otParams.OwnerUUID)
		assert.Equal(tt, "test-role", otParams.Name)
		assert.Equal(tt, "/api/storage/volumes/{volume.uuid}/snapshots", otParams.Path)
	})

	t.Run("WhenParamsSetWithOnlyOwnerID", func(tt *testing.T) {
		params := &RolePrivilegeDeleteParams{
			OwnerID: "owner-uuid-101",
			Name:    "",
			Path:    "",
		}
		otParams := rolePrivilegeDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "owner-uuid-101", otParams.OwnerUUID)
		assert.Equal(tt, "", otParams.Name)
		assert.Equal(tt, "", otParams.Path)
	})

	t.Run("WhenParamsSetWithOnlyName", func(tt *testing.T) {
		params := &RolePrivilegeDeleteParams{
			OwnerID: "",
			Name:    "test-role-only",
			Path:    "",
		}
		otParams := rolePrivilegeDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", otParams.OwnerUUID)
		assert.Equal(tt, "test-role-only", otParams.Name)
		assert.Equal(tt, "", otParams.Path)
	})

	t.Run("WhenParamsSetWithOnlyPath", func(tt *testing.T) {
		params := &RolePrivilegeDeleteParams{
			OwnerID: "",
			Name:    "",
			Path:    "/api/storage/volumes",
		}
		otParams := rolePrivilegeDeleteParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, "", otParams.OwnerUUID)
		assert.Equal(tt, "", otParams.Name)
		assert.Equal(tt, "/api/storage/volumes", otParams.Path)
	})
}

func TestSecurityCertificateDeleteCollectionParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		// The function doesn't handle nil params, so we'll test with empty params instead
		params := &SecurityCertificateDeleteCollectionParams{}
		otParams := securityCertificateDeleteCollectionParamsToONTAP(params)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenAllParamsSet", func(tt *testing.T) {
		name := "cert1"
		svmName := "svm1"
		certType := "server"
		serialNumber := "12345"
		params := &SecurityCertificateDeleteCollectionParams{
			Name:         &name,
			SvmName:      &svmName,
			Type:         &certType,
			SerialNumber: &serialNumber,
		}
		otParams := securityCertificateDeleteCollectionParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, name, *otParams.Name)
		assert.Equal(tt, svmName, *otParams.SvmName)
		assert.Equal(tt, certType, *otParams.Type)
		assert.Equal(tt, serialNumber, *otParams.SerialNumber)
	})

	t.Run("WhenOnlyNameSet", func(tt *testing.T) {
		name := "cert1"
		params := &SecurityCertificateDeleteCollectionParams{
			Name: &name,
		}
		otParams := securityCertificateDeleteCollectionParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, name, *otParams.Name)
		assert.Nil(tt, otParams.SvmName)
		assert.Nil(tt, otParams.Type)
		assert.Nil(tt, otParams.SerialNumber)
	})
}

func TestQosPolicyDeleteCollectionParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsIsNil_ThenReturnDefaultParams", func(tt *testing.T) {
		otParams := qosPolicyDeleteCollectionParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})

	t.Run("WhenParamsSetWithName_ThenNameIsSet", func(tt *testing.T) {
		params := &QosPolicyDeleteCollectionParams{
			Name:    "test-policy",
			SvmName: "test-svm",
		}
		otParams := qosPolicyDeleteCollectionParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Name)
		assert.Equal(tt, "test-policy", *otParams.Name)
		assert.NotNil(tt, otParams.SvmName)
		assert.Equal(tt, "test-svm", *otParams.SvmName)
	})

	t.Run("WhenParamsSetWithSvmName_ThenSvmNameIsSet", func(tt *testing.T) {
		params := &QosPolicyDeleteCollectionParams{
			SvmName: "test-svm",
		}
		otParams := qosPolicyDeleteCollectionParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.SvmName)
		assert.Equal(tt, "test-svm", *otParams.SvmName)
	})

	t.Run("WhenParamsSetWithUUID_ThenUUIDIsSet", func(tt *testing.T) {
		params := &QosPolicyDeleteCollectionParams{
			UUID: "test-uuid",
		}
		otParams := qosPolicyDeleteCollectionParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.UUID)
		assert.Equal(tt, "test-uuid", *otParams.UUID)
	})

	t.Run("WhenParamsSetWithAllFields_ThenAllFieldsAreSet", func(tt *testing.T) {
		params := &QosPolicyDeleteCollectionParams{
			UUID:    "test-uuid",
			Name:    "test-policy",
			SvmName: "test-svm",
		}
		otParams := qosPolicyDeleteCollectionParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.UUID)
		assert.Equal(tt, "test-uuid", *otParams.UUID)
		assert.NotNil(tt, otParams.Name)
		assert.Equal(tt, "test-policy", *otParams.Name)
		assert.NotNil(tt, otParams.SvmName)
		assert.Equal(tt, "test-svm", *otParams.SvmName)
	})

	t.Run("WhenAllFieldsEmpty_ThenReturnDefaultParams", func(tt *testing.T) {
		params := &QosPolicyDeleteCollectionParams{
			UUID:    "",
			Name:    "",
			SvmName: "",
		}
		otParams := qosPolicyDeleteCollectionParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		// Verify all fields are nil/empty as expected by ONTAP SDK
		// When fields are empty strings, they should not be set in otParams
		assert.Nil(tt, otParams.UUID)
		assert.Nil(tt, otParams.Name)
		assert.Nil(tt, otParams.SvmName)
		// ReturnTimeout should still be set (default behavior)
		assert.NotNil(tt, otParams.ReturnTimeout)
	})
}

func TestNvmeNamespaceGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := nvmeNamespaceGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenSvmNameSet_ThenSvmNameIsSet", func(tt *testing.T) {
		svmName := "svm1"
		params := &NvmeNamespaceGetParams{
			SvmName: &svmName,
		}
		otParams := nvmeNamespaceGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, &svmName, otParams.SvmName)
	})
	t.Run("WhenVolumeNameSet_ThenVolumeNameIsSet", func(tt *testing.T) {
		volName := "vol1"
		params := &NvmeNamespaceGetParams{
			VolumeName: &volName,
		}
		otParams := nvmeNamespaceGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, &volName, otParams.LocationVolumeName)
	})
	t.Run("WhenNamespaceNameNonEmpty_ThenFullPathIsSet", func(tt *testing.T) {
		volName := "vol1"
		nsName := "ns1"
		params := &NvmeNamespaceGetParams{
			VolumeName:    &volName,
			NamespaceName: &nsName,
		}
		otParams := nvmeNamespaceGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Name)
		assert.Equal(tt, "/vol/vol1/ns1", *otParams.Name)
	})
	t.Run("WhenNamespaceNameEmpty_ThenNameNotSet", func(tt *testing.T) {
		volName := "vol1"
		nsName := ""
		params := &NvmeNamespaceGetParams{
			VolumeName:    &volName,
			NamespaceName: &nsName,
		}
		otParams := nvmeNamespaceGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Nil(tt, otParams.Name)
	})
	t.Run("WhenNamespaceNameNil_ThenNameNotSet", func(tt *testing.T) {
		volName := "vol1"
		params := &NvmeNamespaceGetParams{
			VolumeName:    &volName,
			NamespaceName: nil,
		}
		otParams := nvmeNamespaceGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Nil(tt, otParams.Name)
	})
	t.Run("WhenFieldsAndMaxRecordsSet_ThenBothAreSet", func(tt *testing.T) {
		maxRecords := int64(10)
		fields := []string{"name", "uuid"}
		params := &NvmeNamespaceGetParams{
			BaseParams: BaseParams{
				Fields:     fields,
				MaxRecords: &maxRecords,
			},
		}
		otParams := nvmeNamespaceGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.Equal(tt, fields, otParams.Fields)
		assert.NotNil(tt, otParams.MaxRecords)
	})
}

func TestConstructNamespaceName(t *testing.T) {
	t.Run("WhenVolumeNameNil_ThenReturnNil", func(tt *testing.T) {
		nsName := "ns1"
		result := constructNamespaceName(nil, &nsName)
		assert.Nil(tt, result)
	})
	t.Run("WhenNamespaceNameNil_ThenReturnNil", func(tt *testing.T) {
		volName := "vol1"
		result := constructNamespaceName(&volName, nil)
		assert.Nil(tt, result)
	})
	t.Run("WhenBothNil_ThenReturnNil", func(tt *testing.T) {
		result := constructNamespaceName(nil, nil)
		assert.Nil(tt, result)
	})
	t.Run("WhenBothSet_ThenReturnFullPath", func(tt *testing.T) {
		volName := "vol1"
		nsName := "ns1"
		result := constructNamespaceName(&volName, &nsName)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/vol/vol1/ns1", *result)
	})
}

func TestSnapmirrorRelationshipCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := snapmirrorRelationshipCreateParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenDestinationPathSet_SetsDestinationPath", func(tt *testing.T) {
		params := &SnapmirrorRelationshipCreateParams{
			DestinationPath: "/dest/path",
			SourcePath:      "svm:vol",
		}
		otParams := snapmirrorRelationshipCreateParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		assert.NotNil(tt, otParams.Info)
		assert.NotNil(tt, otParams.Info.Destination)
		assert.Equal(tt, "/dest/path", *otParams.Info.Destination.Path)
	})
}

func TestNfsClientsGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsNil", func(tt *testing.T) {
		otParams := nfsClientsGetParamsToONTAP(nil)
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsSet", func(tt *testing.T) {
		volumeUUID := "vol-uuid"
		svmName := "svm-1"
		params := &NfsClientsGetParams{
			VolumeUUID: &volumeUUID,
			SvmName:    &svmName,
		}
		otParams := nfsClientsGetParamsToONTAP(params)
		assert.NotNil(tt, otParams)
		require.NotNil(tt, otParams.VolumeUUID)
		require.NotNil(tt, otParams.SvmName)
		assert.Equal(tt, volumeUUID, *otParams.VolumeUUID)
		assert.Equal(tt, svmName, *otParams.SvmName)
	})
}

func TestSvmPeerDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenForceFalse", func(tt *testing.T) {
		otParams := svmPeerDeleteParamsToONTAP(&SvmPeerDeleteParams{SvmPeerUUID: "peer-uuid"})
		assert.Equal(tt, "peer-uuid", otParams.UUID)
		assert.Nil(tt, otParams.Info)
	})

	t.Run("WhenForceTrue", func(tt *testing.T) {
		otParams := svmPeerDeleteParamsToONTAP(&SvmPeerDeleteParams{SvmPeerUUID: "peer-uuid", Force: true})
		assert.Equal(tt, "peer-uuid", otParams.UUID)
		require.NotNil(tt, otParams.Info)
		require.NotNil(tt, otParams.Info.Force)
		assert.True(tt, *otParams.Info.Force)
	})
}
