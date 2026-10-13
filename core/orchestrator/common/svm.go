package common

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// DeriveVCMSvmName returns the data SVM name for a VCM deployment ({deploymentId}-svm-01).
func DeriveVCMSvmName(deploymentName string) (string, error) {
	if deploymentName == "" {
		return "", fmt.Errorf("VCM deployment ID is required to get the data SVM")
	}
	return deploymentName + "-svm-01", nil
}

// PoolNasLifStore loads a pool to read NAS LIF data.
type PoolNasLifStore interface {
	GetPoolByUUID(ctx context.Context, poolUUID string) (*datamodel.Pool, error)
}

// PoolNasLifIPStore loads a pool and persists NAS LIF address changes.
type PoolNasLifIPStore interface {
	PoolNasLifStore
	GetNodesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Node, error)
	GetLifsForNodesWithProtocol(ctx context.Context, nodeIDs []int64, accountID int64, protocol string) ([]*datamodel.Lif, error)
	ApplyPoolNasLifIPUpdates(ctx context.Context, poolUUID string, updates []NasLifIPUpdate) ([]NasLifIPUpdateResult, error)
}

// NasLifIPApplied captures the config value that was replaced and the stored value after prefix preservation.
type NasLifIPApplied struct {
	PreviousIPAddress string
	StoredIPAddress   string
}

type appliedNasLif struct {
	previous string
	stored   string
}

// UpdatePoolNasLifIPs writes each NAS LIF address into the pool VLM config and into matching lifs rows.
func UpdatePoolNasLifIPs(ctx context.Context, store PoolNasLifIPStore, poolUUID string, updates []NasLifIPUpdate) ([]NasLifIPUpdateResult, error) {
	cleaned, err := validateNasLifUpdates(updates)
	if err != nil {
		return nil, err
	}

	results, err := store.ApplyPoolNasLifIPUpdates(ctx, poolUUID, cleaned)
	if err != nil {
		return nil, err
	}

	logger := util.GetLogger(ctx)
	logger.Infof("Updated NAS LIF addresses for pool %s", poolUUID)
	return results, nil
}

// ListPoolNasLifs returns NAS LIFs stored in a pool's VLM config.
func ListPoolNasLifs(ctx context.Context, store PoolNasLifStore, poolUUID string) ([]PoolNasLif, error) {
	pool, err := store.GetPoolByUUID(ctx, poolUUID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrPoolNotFound, err)
		}
		return nil, err
	}
	if pool == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrPoolNotFound, customerrors.NewNotFoundErr("Pool", &poolUUID))
	}

	return listNasLifsFromVLMConfig(pool.VLMConfig)
}

func nasLifWrites(ctx context.Context, store PoolNasLifIPStore, pool *datamodel.Pool, updates []NasLifIPUpdate, applied map[string]appliedNasLif) ([]NasLifIPWrite, map[string][]string, error) {
	nodes, err := store.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return nil, nil, err
	}
	nodeIDs := make([]int64, 0, len(nodes))
	for _, node := range nodes {
		if node != nil && node.ID > 0 {
			nodeIDs = append(nodeIDs, node.ID)
		}
	}
	if len(nodeIDs) == 0 {
		_, locations := BuildNasLifWrites(nil, updates, nil)
		return nil, locations, nil
	}

	lifs, err := store.GetLifsForNodesWithProtocol(ctx, nodeIDs, pool.AccountID, "")
	if err != nil {
		return nil, nil, err
	}

	exportedApplied := make(map[string]NasLifIPApplied, len(applied))
	for lifName, change := range applied {
		exportedApplied[lifName] = NasLifIPApplied{
			PreviousIPAddress: change.previous,
			StoredIPAddress:   change.stored,
		}
	}
	writes, locations := BuildNasLifWrites(lifs, updates, exportedApplied)
	return writes, locations, nil
}

// BuildNasLifWrites returns the lifs-row updates and reported locations for NAS LIF config changes.
func BuildNasLifWrites(lifs []*datamodel.Lif, updates []NasLifIPUpdate, applied map[string]NasLifIPApplied) ([]NasLifIPWrite, map[string][]string) {
	locations := make(map[string][]string, len(updates))
	for _, update := range updates {
		locations[update.LifName] = []string{NasLifLocationVLMConfig}
	}

	var writes []NasLifIPWrite
	seenUUID := map[string]struct{}{}
	for _, update := range updates {
		for _, lif := range matchingNasLifs(lifs, update.LifName) {
			if lif.UUID == "" {
				continue
			}
			if _, ok := seenUUID[lif.UUID]; ok {
				continue
			}
			seenUUID[lif.UUID] = struct{}{}
			writes = append(writes, NasLifIPWrite{
				UUID:      lif.UUID,
				IPAddress: hostOnly(applied[update.LifName].StoredIPAddress),
			})
			if !containsLocation(locations[update.LifName], NasLifLocationLifs) {
				locations[update.LifName] = append(locations[update.LifName], NasLifLocationLifs)
			}
		}
	}
	return writes, locations
}

// ApplyNasLifIPsToConfig updates NAS LIF addresses in a pool VLM config.
func ApplyNasLifIPsToConfig(raw string, updates []NasLifIPUpdate) (string, map[string]NasLifIPApplied, error) {
	updatedConfig, applied, err := applyNasLifIPs(raw, updates)
	if err != nil {
		return "", nil, err
	}
	exportedApplied := make(map[string]NasLifIPApplied, len(applied))
	for lifName, change := range applied {
		exportedApplied[lifName] = NasLifIPApplied{
			PreviousIPAddress: change.previous,
			StoredIPAddress:   change.stored,
		}
	}
	return updatedConfig, exportedApplied, nil
}

func matchingNasLifs(lifs []*datamodel.Lif, name string) []*datamodel.Lif {
	var matched []*datamodel.Lif
	for _, lif := range lifs {
		if lif == nil || lif.Name != name || !isNasProtocol(lif) {
			continue
		}
		matched = append(matched, lif)
	}
	return matched
}

func isNasProtocol(lif *datamodel.Lif) bool {
	if lif.LifDetails == nil || strings.TrimSpace(lif.LifDetails.ProtocolType) == "" {
		return true
	}
	return lif.LifDetails.ProtocolType == string(vlm.LIFTypeNas)
}

func containsLocation(locations []string, location string) bool {
	for _, existing := range locations {
		if existing == location {
			return true
		}
	}
	return false
}

func validateNasLifUpdates(updates []NasLifIPUpdate) ([]NasLifIPUpdate, error) {
	if len(updates) == 0 {
		return nil, nasLifValidationErr("at least one NAS LIF address is required")
	}
	cleaned := make([]NasLifIPUpdate, 0, len(updates))
	seen := make(map[string]struct{}, len(updates))
	for _, update := range updates {
		name := strings.TrimSpace(update.LifName)
		ip := strings.TrimSpace(update.IPAddress)
		if name == "" {
			return nil, nasLifValidationErr("NAS LIF name is required")
		}
		if !isIPv4(ip) {
			return nil, nasLifValidationErr(fmt.Sprintf("NAS LIF %s requires an IPv4 address", name))
		}
		if _, ok := seen[name]; ok {
			return nil, nasLifValidationErr(fmt.Sprintf("NAS LIF %s is listed more than once", name))
		}
		seen[name] = struct{}{}
		cleaned = append(cleaned, NasLifIPUpdate{LifName: name, IPAddress: ip})
	}
	return cleaned, nil
}

func applyNasLifIPs(raw string, updates []NasLifIPUpdate) (string, map[string]appliedNasLif, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil, nasLifValidationErr("pool VLM config is empty")
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil || root == nil {
		return "", nil, nasLifValidationErr("pool VLM config is not valid JSON")
	}
	svmRaw, ok := root["svm"]
	if !ok || isJSONNull(svmRaw) {
		return "", nil, nasLifValidationErr("pool VLM config has no SVM NAS LIFs")
	}
	var svms map[string]json.RawMessage
	if err := json.Unmarshal(svmRaw, &svms); err != nil || svms == nil {
		return "", nil, nasLifValidationErr("pool VLM config SVM section is invalid")
	}

	wanted := make(map[string]string, len(updates))
	for _, update := range updates {
		wanted[update.LifName] = update.IPAddress
	}
	applied := make(map[string]appliedNasLif, len(updates))

	svmNames := make([]string, 0, len(svms))
	for name := range svms {
		svmNames = append(svmNames, name)
	}
	sort.Strings(svmNames)

	changed := false
	for _, svmName := range svmNames {
		updated, svmChanged, err := applyToSVM(svms[svmName], wanted, applied)
		if err != nil {
			return "", nil, err
		}
		if svmChanged {
			svms[svmName] = updated
			changed = true
		}
	}

	var missing []string
	for _, update := range updates {
		if _, ok := applied[update.LifName]; !ok {
			missing = append(missing, update.LifName)
		}
	}
	if len(missing) > 0 {
		return "", nil, nasLifValidationErr(fmt.Sprintf("NAS LIF %s was not found in the pool VLM config", strings.Join(missing, ", ")))
	}
	if !changed {
		return raw, applied, nil
	}

	svmBytes, err := json.Marshal(svms)
	if err != nil {
		return "", nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("encode pool VLM config SVMs: %w", err))
	}
	root["svm"] = svmBytes
	out, err := json.Marshal(root)
	if err != nil {
		return "", nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("encode pool VLM config: %w", err))
	}
	return string(out), applied, nil
}

func listNasLifsFromVLMConfig(raw string) ([]PoolNasLif, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil || root == nil {
		return nil, nasLifValidationErr("pool VLM config is not valid JSON")
	}
	svmRaw, ok := root["svm"]
	if !ok || isJSONNull(svmRaw) {
		return nil, nil
	}
	var svms map[string]json.RawMessage
	if err := json.Unmarshal(svmRaw, &svms); err != nil || svms == nil {
		return nil, nasLifValidationErr("pool VLM config SVM section is invalid")
	}

	svmNames := make([]string, 0, len(svms))
	for name := range svms {
		svmNames = append(svmNames, name)
	}
	sort.Strings(svmNames)

	var result []PoolNasLif
	for _, svmName := range svmNames {
		items, err := listNasLifsFromSVM(svmName, svms[svmName])
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	return result, nil
}

func listNasLifsFromSVM(svmName string, raw json.RawMessage) ([]PoolNasLif, error) {
	var svm map[string]json.RawMessage
	if err := json.Unmarshal(raw, &svm); err != nil || svm == nil {
		return nil, nasLifValidationErr("pool VLM config contains an invalid SVM")
	}
	lifsRaw, ok := svm["svm_lifs"]
	if !ok || isJSONNull(lifsRaw) {
		return nil, nil
	}
	var lifs map[string]json.RawMessage
	if err := json.Unmarshal(lifsRaw, &lifs); err != nil || lifs == nil {
		return nil, nasLifValidationErr("pool VLM config contains invalid SVM LIFs")
	}

	var result []PoolNasLif
	for _, lifType := range []string{string(vlm.LIFTypeIlbNas), string(vlm.LIFTypeNas)} {
		groupRaw, ok := lifs[lifType]
		if !ok || isJSONNull(groupRaw) {
			continue
		}
		items, err := listNasLifsFromGroup(svmName, lifType, groupRaw)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	return result, nil
}

func listNasLifsFromGroup(svmName, lifType string, raw json.RawMessage) ([]PoolNasLif, error) {
	var lifs []json.RawMessage
	if err := json.Unmarshal(raw, &lifs); err != nil {
		return nil, nasLifValidationErr("pool VLM config contains an invalid NAS LIF list")
	}

	result := make([]PoolNasLif, 0, len(lifs))
	for _, lifRaw := range lifs {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(lifRaw, &fields); err != nil || fields == nil {
			return nil, nasLifValidationErr("pool VLM config contains an invalid NAS LIF")
		}
		name, ok := jsonString(fields["lif_name"])
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		ip, _ := jsonString(fields["ip"])
		result = append(result, PoolNasLif{
			SVMName:   svmName,
			LifName:   name,
			LifType:   lifType,
			IPAddress: ip,
		})
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].LifName < result[j].LifName
	})
	return result, nil
}

func applyToSVM(raw json.RawMessage, wanted map[string]string, applied map[string]appliedNasLif) (json.RawMessage, bool, error) {
	var svm map[string]json.RawMessage
	if err := json.Unmarshal(raw, &svm); err != nil || svm == nil {
		return nil, false, nasLifValidationErr("pool VLM config contains an invalid SVM")
	}
	lifsRaw, ok := svm["svm_lifs"]
	if !ok || isJSONNull(lifsRaw) {
		return raw, false, nil
	}
	var lifs map[string]json.RawMessage
	if err := json.Unmarshal(lifsRaw, &lifs); err != nil || lifs == nil {
		return nil, false, nasLifValidationErr("pool VLM config contains invalid SVM LIFs")
	}

	changed := false
	for _, lifType := range []string{string(vlm.LIFTypeIlbNas), string(vlm.LIFTypeNas)} {
		groupRaw, ok := lifs[lifType]
		if !ok || isJSONNull(groupRaw) {
			continue
		}
		updated, groupChanged, err := applyToLIFGroup(groupRaw, wanted, applied)
		if err != nil {
			return nil, false, err
		}
		if groupChanged {
			lifs[lifType] = updated
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	lifsBytes, err := json.Marshal(lifs)
	if err != nil {
		return nil, false, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("encode SVM LIFs: %w", err))
	}
	svm["svm_lifs"] = lifsBytes
	svmBytes, err := json.Marshal(svm)
	if err != nil {
		return nil, false, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("encode SVM config: %w", err))
	}
	return svmBytes, true, nil
}

func applyToLIFGroup(raw json.RawMessage, wanted map[string]string, applied map[string]appliedNasLif) (json.RawMessage, bool, error) {
	var lifs []json.RawMessage
	if err := json.Unmarshal(raw, &lifs); err != nil {
		return nil, false, nasLifValidationErr("pool VLM config contains an invalid NAS LIF list")
	}
	changed := false
	for i, lifRaw := range lifs {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(lifRaw, &fields); err != nil || fields == nil {
			return nil, false, nasLifValidationErr("pool VLM config contains an invalid NAS LIF")
		}
		name, ok := jsonString(fields["lif_name"])
		if !ok {
			continue
		}
		nextIP, wantedName := wanted[name]
		if !wantedName {
			continue
		}
		previous, _ := jsonString(fields["ip"])
		stored := withPreservedPrefix(previous, nextIP)
		ipRaw, err := json.Marshal(stored)
		if err != nil {
			return nil, false, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("encode NAS LIF IP: %w", err))
		}
		fields["ip"] = ipRaw
		updated, err := json.Marshal(fields)
		if err != nil {
			return nil, false, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("encode NAS LIF: %w", err))
		}
		lifs[i] = updated
		changed = true
		if _, seen := applied[name]; !seen {
			applied[name] = appliedNasLif{previous: previous, stored: stored}
		}
	}
	if !changed {
		return raw, false, nil
	}
	out, err := json.Marshal(lifs)
	if err != nil {
		return nil, false, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("encode NAS LIF list: %w", err))
	}
	return out, true, nil
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || isJSONNull(raw) {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func withPreservedPrefix(previous, next string) string {
	if i := strings.Index(previous, "/"); i >= 0 && i < len(previous)-1 {
		return next + previous[i:]
	}
	return next
}

func hostOnly(stored string) string {
	if i := strings.Index(stored, "/"); i >= 0 {
		return stored[:i]
	}
	return stored
}

func isIPv4(value string) bool {
	ip := net.ParseIP(value)
	return ip != nil && ip.To4() != nil
}

func nasLifValidationErr(message string) error {
	return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, customerrors.NewUserInputValidationErr(message))
}
