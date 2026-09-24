package vsa

import (
	"strings"

	ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// GetAllInitiators returns IQN names from an ONTAP igroup initiator list.
func GetAllInitiators(initiators []*ontapModels.IgroupInlineInitiatorsInlineArrayItem) []string {
	names := make([]string, 0, len(initiators))
	for _, initiator := range initiators {
		if initiator == nil || initiator.Name == nil {
			continue
		}
		names = append(names, *initiator.Name)
	}
	return names
}

// IgroupMatchesCreateRequest reports whether an existing ONTAP igroup matches the requested osType and IQNs.
func IgroupMatchesCreateRequest(igroup *ontapRest.Igroup, requestedOsType string, requestedIQNs []string) bool {
	if igroup == nil {
		return false
	}
	if mapOntapOsTypeToAPIEnum(igroup.OsType) != mapOntapOsTypeToAPIEnum(&requestedOsType) {
		return false
	}
	toAdd, toDelete := utils.GetArrayDiff(GetAllInitiators(igroup.IgroupInlineInitiators), requestedIQNs)
	return len(toAdd) == 0 && len(toDelete) == 0
}

// SyncIgroupInitiators reconciles an ONTAP igroup's initiator list to desiredHosts.
func SyncIgroupInitiators(provider Provider, igroupName string, desiredHosts []string, svm *string) (found bool, osType string, err error) {
	exists, hostGroup, err := provider.IgroupExists(igroupName, svm)
	if err != nil {
		return false, "", err
	}
	if !exists || hostGroup == nil || hostGroup.UUID == nil {
		return false, "", nil
	}
	osType = mapOntapOsTypeToAPIEnum(hostGroup.OsType)

	initiatorsToAdd, initiatorsToDelete := utils.GetArrayDiff(GetAllInitiators(hostGroup.IgroupInlineInitiators), desiredHosts)

	// Delete obsolete initiators before adding new ones so replacements at the host
	// limit (e.g. 128) never pass through an over-capacity intermediate state.
	for _, initiatorToDelete := range initiatorsToDelete {
		err = provider.IgroupDeleteInitiator(IgroupDeleteInitiator{
			InitiatorName: initiatorToDelete,
			IgroupUUID:    *hostGroup.UUID,
		})
		if err != nil {
			return true, osType, err
		}
	}

	if len(initiatorsToAdd) > 0 {
		err = provider.IgroupAddInitiator(IgroupAddInitiator{
			Initiator:  initiatorsToAdd,
			IgroupUUID: *hostGroup.UUID,
		})
		if err != nil {
			return true, osType, err
		}
	}
	return true, osType, nil
}

// mapOntapOsTypeToAPIEnum converts an ONTAP igroup os_type value to the API osType enum.
func mapOntapOsTypeToAPIEnum(osType *string) string {
	if osType == nil {
		return ""
	}
	return utils.MapONTAPOSTypeToAPIEnum(*osType)
}

// IgroupCreate creates an initiator group by calling the ONTAP REST Client
func (rc *OntapRestProvider) IgroupCreate(params IgroupCreateParams) (string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return "", err
	}
	iGroupName, err := client.SAN().IGroupCreate(&ontapRest.IgroupCreateParams{
		Name:       params.IgroupName,
		SvmName:    params.SvmName,
		OsType:     params.OsType,
		Initiators: params.Initiator,
	})
	if err != nil {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return iGroupName, nil
}

// IgroupDelete deletes an initiator group by calling the ONTAP REST Client
func (rc *OntapRestProvider) IgroupDelete(uuid string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	err = client.SAN().IGroupDelete(&ontapRest.IgroupDeleteParams{
		UUID: uuid,
	})
	if err != nil && !strings.Contains(err.Error(), "was not found") {
		if strings.Contains(err.Error(), "LUN maps for this initiator group exist") {
			return vsaerrors.NewVCPError(vsaerrors.ErrHostGroupHasLUNMaps, err)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return nil
}

// igroupReadFields are the ONTAP fields needed to map an igroup to a HostGroup.
var igroupReadFields = []string{"name", "os_type", "initiators"}

// IgroupGet fetches a single initiator group by calling the ONTAP REST Client.
func (rc *OntapRestProvider) IgroupGet(name, svmName *string) (*ontapRest.Igroup, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	iGroup, err := client.SAN().IGroupGet(&ontapRest.IgroupGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: igroupReadFields,
		},
		Name:    name,
		SvmName: svmName,
	})
	if err != nil {
		return nil, err
	}
	return iGroup, nil
}

// IgroupNamesForLunUUID returns igroup names mapped to the given LUN UUID on the SVM.
func (rc *OntapRestProvider) IgroupNamesForLunUUID(svmName, lunUUID string) ([]string, error) {
	if strings.TrimSpace(svmName) == "" || strings.TrimSpace(lunUUID) == "" {
		return nil, errors.New("svm name and lun UUID are required")
	}

	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	igroups, err := client.SAN().IGroupsGet(&ontapRest.IgroupGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"name"},
		},
		SvmName:        &svmName,
		LunMapsLunUUID: &lunUUID,
	})
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	names := make([]string, 0, len(igroups))
	for _, igroup := range igroups {
		if igroup == nil || igroup.Name == nil {
			continue
		}
		name := strings.TrimSpace(*igroup.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// IgroupList lists initiator groups on the given SVM.
func (rc *OntapRestProvider) IgroupList(svm *string) ([]*ontapRest.Igroup, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	igroups, err := client.SAN().IGroupsGet(&ontapRest.IgroupGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: igroupReadFields,
		},
		SvmName: svm,
	})
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return igroups, nil
}

func (rc *OntapRestProvider) IgroupExists(name string, svm *string) (bool, *ontapRest.Igroup, error) {
	res, err := rc.IgroupGet(&name, svm)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, nil, nil
		}
		return false, nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if res == nil {
		return false, nil, nil
	}

	return true, res, nil
}

func (rc *OntapRestProvider) IgroupAddInitiator(params IgroupAddInitiator) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	err = client.SAN().IGroupAddInitiator(&ontapRest.IgroupAddInitiatorParams{
		InitiatorQNs: params.Initiator,
		IgroupUUID:   params.IgroupUUID,
	})

	if err != nil && !strings.Contains(err.Error(), "already contains initiator") {
		return err
	}

	return nil
}

func (rc *OntapRestProvider) IgroupDeleteInitiator(params IgroupDeleteInitiator) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	err = client.SAN().IGroupDeleteInitiator(&ontapRest.IgroupDeleteInitiatorParams{
		InitiatorIQNName: params.InitiatorName,
		IgroupUUID:       params.IgroupUUID,
	})

	if err != nil && !strings.Contains(err.Error(), "does not contain initiator") {
		return err
	}

	return nil
}
