package backgroundactivities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	certutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/certificate"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const defaultMinimumIssuingCARemainingLifetime = "2160h" // 90 days / 3 months

type certificateRotationDependencies struct {
	resolveCloudService             func(context.Context) (hyperscaler.Services, error)
	getProvider                     func(context.Context, *datamodel.Pool, []*datamodel.Node, int, string) (vsa.Provider, error)
	getCertificateAndPrivateKeyByID func(hyperscaler.Services, string, string, string, string, string) (*hyperscalermodels.CustomCertificateResponse, error)
	generateExpertCertificate       func(hyperscaler.Services, string, string, *datamodel.PoolCredentials, bool) (*hyperscalermodels.CustomCertificateResponse, error)
	installExpertClientTrust        func(context.Context, *datamodel.Pool, interface{}, interface{}) error
	getHyperscaler                  func() string
	now                             func() time.Time
}

func defaultCertificateRotationDependencies() *certificateRotationDependencies {
	return &certificateRotationDependencies{
		resolveCloudService:             getGcpServiceForCerts,
		getProvider:                     getRotationProvider,
		getCertificateAndPrivateKeyByID: vsa.GetCertificateAndPrivateKeyByID,
		generateExpertCertificate:       generateAndCreateCertificateForVSACluster,
		getHyperscaler:                  env.GetHyperscaler,
		now:                             time.Now,
	}
}

func (a *RotateVcpToVsaCertificateActivity) dependencies() *certificateRotationDependencies {
	if a.deps == nil {
		a.deps = defaultCertificateRotationDependencies()
		a.deps.installExpertClientTrust = a.installExpertClientTrustWithPasswordAuth
		return a.deps
	}
	defaults := defaultCertificateRotationDependencies()
	if a.deps.resolveCloudService == nil {
		a.deps.resolveCloudService = defaults.resolveCloudService
	}
	if a.deps.getProvider == nil {
		a.deps.getProvider = defaults.getProvider
	}
	if a.deps.getCertificateAndPrivateKeyByID == nil {
		a.deps.getCertificateAndPrivateKeyByID = defaults.getCertificateAndPrivateKeyByID
	}
	if a.deps.generateExpertCertificate == nil {
		a.deps.generateExpertCertificate = defaults.generateExpertCertificate
	}
	if a.deps.installExpertClientTrust == nil {
		a.deps.installExpertClientTrust = a.installExpertClientTrustWithPasswordAuth
	}
	if a.deps.getHyperscaler == nil {
		a.deps.getHyperscaler = defaults.getHyperscaler
	}
	if a.deps.now == nil {
		a.deps.now = defaults.now
	}
	return a.deps
}

func getRotationProvider(
	ctx context.Context,
	pool *datamodel.Pool,
	nodes []*datamodel.Node,
	authType int,
	username string,
) (vsa.Provider, error) {
	password := pool.PoolCredentials.Password
	if password == "" {
		var err error
		password, err = vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretID)
		if err != nil {
			return nil, err
		}
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:          nodes,
		DeploymentName: pool.DeploymentName,
		OntapCredentials: &datamodel.PoolCredentials{
			AuthType: authType,
			Username: username,
			Password: password,
			SecretID: pool.PoolCredentials.SecretID,
		},
	})
	return vsa.GetProviderByNode(ctx, node)
}

func validateCertificateAuthority(
	ca *hyperscalermodels.CertificateAuthority,
	now time.Time,
	minimumRemaining time.Duration,
) error {
	if ca == nil {
		return errors.New("certificate authority metadata is nil")
	}
	if ca.State != "ENABLED" {
		return fmt.Errorf("certificate authority state is %q, expected ENABLED", ca.State)
	}
	if !ca.ExpireTime.After(now) {
		return fmt.Errorf("certificate authority expired at %s", ca.ExpireTime.UTC().Format(time.RFC3339))
	}
	remaining := ca.ExpireTime.Sub(now)
	if remaining < minimumRemaining {
		return fmt.Errorf("certificate authority remaining lifetime %s is below required floor %s", remaining, minimumRemaining)
	}
	return nil
}

func validateStagedCertificateForReuse(
	certificate *hyperscalermodels.CustomCertificate,
	now time.Time,
	rotationThreshold float64,
) error {
	if certificate == nil {
		return errors.New("staged certificate is nil")
	}
	block, rest := pem.Decode([]byte(strings.TrimSpace(certificate.PemCertificate)))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("staged certificate is not exactly one PEM certificate")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse staged certificate: %w", err)
	}
	if now.Before(parsed.NotBefore) {
		return fmt.Errorf("staged certificate is not valid before %s", parsed.NotBefore.UTC().Format(time.RFC3339))
	}
	if !now.Before(parsed.NotAfter) {
		return fmt.Errorf("staged certificate expired at %s", parsed.NotAfter.UTC().Format(time.RFC3339))
	}
	if rotationThreshold <= 0 || rotationThreshold > 1 {
		return fmt.Errorf("invalid certificate rotation threshold %f", rotationThreshold)
	}
	lifetime := parsed.NotAfter.Sub(parsed.NotBefore)
	if now.Sub(parsed.NotBefore) >= time.Duration(float64(lifetime)*rotationThreshold) {
		return errors.New("staged certificate reached the normal rotation threshold")
	}
	return nil
}

func validateCertificateForExpectedIssuer(
	certificate *hyperscalermodels.CustomCertificate,
	expectedCAURI, expectedRegion string,
) error {
	if certificate == nil {
		return errors.New("certificate issuer metadata is nil")
	}
	parts := strings.Split(expectedCAURI, "/")
	if len(parts) != 3 ||
		strings.TrimSpace(parts[0]) == "" ||
		strings.TrimSpace(parts[1]) == "" ||
		strings.TrimSpace(parts[2]) == "" {
		return fmt.Errorf("invalid expected CA URI %q", expectedCAURI)
	}
	expectedProject := strings.TrimSpace(parts[0])
	expectedPool := strings.TrimSpace(parts[1])
	expectedCA := strings.TrimSpace(parts[2])
	expectedRegion = strings.TrimSpace(expectedRegion)

	for _, identity := range []struct {
		field    string
		actual   string
		expected string
	}{
		{field: "certificate project", actual: certificate.CertOwningEntity, expected: expectedProject},
		{field: "CA pool", actual: certificate.CaGroupName, expected: expectedPool},
		{field: "CA name", actual: certificate.CaName, expected: expectedCA},
		{field: "region", actual: certificate.Region, expected: expectedRegion},
	} {
		actual := strings.TrimSpace(identity.actual)
		if actual != "" && actual != identity.expected {
			return fmt.Errorf("%s %q does not match expected %q", identity.field, actual, identity.expected)
		}
	}

	structuredIdentityProven :=
		strings.TrimSpace(certificate.CertOwningEntity) != "" &&
			strings.TrimSpace(certificate.CaGroupName) != "" &&
			strings.TrimSpace(certificate.CaName) != ""
	issuerIdentityProven := false
	if issuer := strings.Trim(strings.TrimSpace(certificate.IssuerCertificateAuthority), "/"); issuer != "" {
		issuerParts := strings.Split(issuer, "/")
		if len(issuerParts) != 8 ||
			issuerParts[0] != "projects" ||
			issuerParts[2] != "locations" ||
			issuerParts[4] != "caPools" ||
			issuerParts[6] != "certificateAuthorities" {
			return fmt.Errorf("certificate issuer authority %q has an invalid format", certificate.IssuerCertificateAuthority)
		}
		if issuerParts[1] != expectedProject ||
			issuerParts[3] != expectedRegion ||
			issuerParts[5] != expectedPool ||
			issuerParts[7] != expectedCA {
			return fmt.Errorf(
				"certificate issuer authority %q does not match expected CA %q in region %q",
				certificate.IssuerCertificateAuthority,
				expectedCAURI,
				expectedRegion,
			)
		}
		issuerIdentityProven = true
	}
	if !structuredIdentityProven && !issuerIdentityProven {
		return errors.New("certificate issuer identity cannot be proven from cloud metadata")
	}
	return nil
}

func validateCertificateForReuse(
	certificate *hyperscalermodels.CustomCertificate,
	expectedCAURI, expectedRegion string,
	now time.Time,
	rotationThreshold float64,
) error {
	if err := validateCertificateForExpectedIssuer(certificate, expectedCAURI, expectedRegion); err != nil {
		return err
	}
	return validateStagedCertificateForReuse(certificate, now, rotationThreshold)
}

type parsedPEMCertificate struct {
	pemValue string
	cert     *x509.Certificate
}

func normalizeCertificateAuthorityChain(chain []string) ([]parsedPEMCertificate, error) {
	if len(chain) == 0 {
		return nil, errors.New("certificate authority PEM chain is empty")
	}
	bySubject := make(map[string]parsedPEMCertificate)
	seenDER := make(map[string]struct{})
	for index, pemValue := range chain {
		block, rest := pem.Decode([]byte(strings.TrimSpace(pemValue)))
		if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
			return nil, fmt.Errorf("certificate authority chain entry %d is not exactly one PEM certificate", index)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate authority chain entry %d: %w", index, err)
		}
		fingerprint := sha256.Sum256(certificate.Raw)
		fingerprintKey := hex.EncodeToString(fingerprint[:])
		if _, duplicate := seenDER[fingerprintKey]; duplicate {
			continue
		}
		seenDER[fingerprintKey] = struct{}{}
		subject := string(certificate.RawSubject)
		if _, duplicateSubject := bySubject[subject]; duplicateSubject {
			return nil, fmt.Errorf("certificate authority chain has multiple certificates for subject %q", certificate.Subject)
		}
		bySubject[subject] = parsedPEMCertificate{
			pemValue: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})),
			cert:     certificate,
		}
	}
	if len(bySubject) == 0 {
		return nil, errors.New("certificate authority PEM chain contains no certificates")
	}

	var roots []parsedPEMCertificate
	for _, candidate := range bySubject {
		if bytes.Equal(candidate.cert.RawSubject, candidate.cert.RawIssuer) {
			if err := candidate.cert.CheckSignatureFrom(candidate.cert); err != nil {
				return nil, fmt.Errorf("self-issued root %q has invalid signature: %w", candidate.cert.Subject, err)
			}
			roots = append(roots, candidate)
		}
	}
	if len(roots) != 1 {
		return nil, fmt.Errorf("certificate authority PEM chain must contain exactly one self-signed root, found %d", len(roots))
	}

	ordered := []parsedPEMCertificate{roots[0]}
	used := map[string]struct{}{string(roots[0].cert.RawSubject): {}}
	for len(ordered) < len(bySubject) {
		parent := ordered[len(ordered)-1]
		var children []parsedPEMCertificate
		for subject, candidate := range bySubject {
			if _, alreadyUsed := used[subject]; alreadyUsed {
				continue
			}
			if bytes.Equal(candidate.cert.RawIssuer, parent.cert.RawSubject) {
				children = append(children, candidate)
			}
		}
		if len(children) != 1 {
			return nil, fmt.Errorf("certificate authority PEM chain is disconnected or ambiguous below %q", parent.cert.Subject)
		}
		if err := children[0].cert.CheckSignatureFrom(parent.cert); err != nil {
			return nil, fmt.Errorf("certificate authority chain signature verification failed: %w", err)
		}
		ordered = append(ordered, children[0])
		used[string(children[0].cert.RawSubject)] = struct{}{}
	}
	return ordered, nil
}

func certificateFingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

func verifyInstalledCertificate(expected *x509.Certificate, installed []*vsa.ServerCertificateResponse) error {
	for _, candidate := range installed {
		if candidate == nil || strings.TrimSpace(candidate.PublicCertificate) == "" {
			continue
		}
		block, rest := pem.Decode([]byte(candidate.PublicCertificate))
		if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
			continue
		}
		if bytes.Equal(expected.Raw, block.Bytes) {
			return nil
		}
	}
	return fmt.Errorf("installed certificate does not match expected fingerprint %s", certificateFingerprint(expected))
}

func installAndVerifyCertificateAuthorityChain(provider vsa.Provider, chain []parsedPEMCertificate) error {
	for _, entry := range chain {
		fingerprint := certificateFingerprint(entry.cert)
		clientCAName := "vcp-ca-" + fingerprint[:16]
		if err := installAndVerifyClientCA(provider, entry, clientCAName); err != nil {
			return err
		}
		serverCAName := "vcp-server-ca-" + fingerprint[:16]
		if err := installAndVerifyServerCA(provider, entry, serverCAName); err != nil {
			return err
		}
	}
	return nil
}

func installAndVerifyClientCA(provider vsa.Provider, entry parsedPEMCertificate, name string) error {
	_, installErr := provider.InstallClientCA(vsa.InstallClientCAParams{
		CertificateName: name,
		Certificate:     entry.pemValue,
		CommonName:      entry.cert.Subject.CommonName,
	})
	if installErr != nil && !isDuplicateCertificateError(installErr) {
		return fmt.Errorf("install client_ca %s: %w", name, installErr)
	}
	return verifyCertificateAuthorityTrust(provider, entry.cert, name, vsa.TrustCertificateTypeClientCA)
}

func installAndVerifyServerCA(provider vsa.Provider, entry parsedPEMCertificate, name string) error {
	_, installErr := provider.InstallServerCA(vsa.InstallServerCAParams{
		CertificateName: name,
		Certificate:     entry.pemValue,
		CommonName:      entry.cert.Subject.CommonName,
	})
	if installErr != nil && !isDuplicateCertificateError(installErr) {
		return fmt.Errorf("install server_ca %s: %w", name, installErr)
	}
	return verifyCertificateAuthorityTrust(provider, entry.cert, name, vsa.TrustCertificateTypeServerCA)
}

func verifyCertificateAuthorityTrust(
	provider vsa.Provider,
	expected *x509.Certificate,
	name, certificateType string,
) error {
	installed, err := provider.GetServerCertificates(vsa.GetServerCertificateParams{
		CertificateName: name,
		CertificateType: certificateType,
		Fields:          []string{"uuid", "name", "type", "public_certificate"},
	})
	if err != nil {
		return fmt.Errorf("read back %s %s: %w", certificateType, name, err)
	}
	if err := verifyInstalledCertificate(expected, installed); err != nil {
		return fmt.Errorf("verify %s %s: %w", certificateType, name, err)
	}
	return nil
}

func hasCertificateApplications(account *vsa.SecurityAccount) bool {
	if account == nil {
		return false
	}
	required := map[string]map[string]bool{
		"http":   {"password": false, "certificate": false},
		"ontapi": {"password": false, "certificate": false},
	}
	for _, application := range account.Applications {
		methods, needed := required[application.Application]
		if !needed {
			continue
		}
		for _, method := range application.AuthenticationMethods {
			if _, requiredMethod := methods[method]; requiredMethod {
				methods[method] = true
			}
		}
	}
	return required["http"]["password"] &&
		required["http"]["certificate"] &&
		required["ontapi"]["password"] &&
		required["ontapi"]["certificate"]
}

func mergeCertificateApplications(
	applications []ontaprest.SecurityAccountApplication,
) []ontaprest.SecurityAccountApplication {
	merged := make([]ontaprest.SecurityAccountApplication, 0, len(applications)+2)
	found := map[string]bool{"http": false, "ontapi": false}
	for _, application := range applications {
		copied := ontaprest.SecurityAccountApplication{
			Application: application.Application,
		}
		seenMethods := make(map[string]struct{}, len(application.AuthenticationMethods)+2)
		for _, method := range application.AuthenticationMethods {
			if _, duplicate := seenMethods[method]; duplicate {
				continue
			}
			seenMethods[method] = struct{}{}
			copied.AuthenticationMethods = append(copied.AuthenticationMethods, method)
		}
		if _, requiredApplication := found[application.Application]; requiredApplication {
			found[application.Application] = true
			for _, method := range []string{"password", "certificate"} {
				if _, exists := seenMethods[method]; !exists {
					copied.AuthenticationMethods = append(copied.AuthenticationMethods, method)
				}
			}
		}
		merged = append(merged, copied)
	}
	for _, application := range []string{"http", "ontapi"} {
		if !found[application] {
			merged = append(merged, ontaprest.SecurityAccountApplication{
				Application:           application,
				AuthenticationMethods: []string{"password", "certificate"},
			})
		}
	}
	return merged
}

func ensureAndVerifyCertificateLogin(provider vsa.Provider, username string) error {
	accountProvider, ok := provider.(vsa.ExpertModeAccountProvider)
	if !ok {
		return errors.New("VSA provider does not support security account verification")
	}
	account, err := accountProvider.GetSecurityAccount(vsa.GetSecurityAccountParams{Name: username})
	if err != nil {
		return fmt.Errorf("get security account %q: %w", username, err)
	}
	if account == nil {
		return fmt.Errorf("security account %q does not exist", username)
	}
	if !hasCertificateApplications(account) {
		ownerUUID := account.OwnerUUID
		if ownerUUID == "" {
			ownerUUID, err = accountProvider.GetClusterUUID()
			if err != nil {
				return fmt.Errorf("get cluster UUID for security account repair: %w", err)
			}
		}
		err = accountProvider.ModifySecurityAccount(vsa.ModifySecurityAccountParams{
			Name:         username,
			OwnerUUID:    ownerUUID,
			Applications: mergeCertificateApplications(account.Applications),
		})
		if err != nil {
			return fmt.Errorf("repair security account %q certificate applications: %w", username, err)
		}
		account, err = accountProvider.GetSecurityAccount(vsa.GetSecurityAccountParams{Name: username})
		if err != nil {
			return fmt.Errorf("read back repaired security account %q: %w", username, err)
		}
	}
	if !hasCertificateApplications(account) {
		return fmt.Errorf("security account %q does not enable certificate authentication for http and ontapi", username)
	}
	return nil
}

func parseTargetCAURI(targetCAURI string) (projectID, poolName, caName string, err error) {
	parts := strings.Split(targetCAURI, "/")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid target CA URI %q", targetCAURI)
	}
	projectID = strings.TrimSpace(parts[0])
	poolName = strings.TrimSpace(parts[1])
	caName = strings.TrimSpace(parts[2])
	if projectID == "" || poolName == "" || caName == "" {
		return "", "", "", fmt.Errorf("invalid target CA URI %q", targetCAURI)
	}
	return projectID, poolName, caName, nil
}

func (a *RotateVcpToVsaCertificateActivity) preflightCertificateIssuance(
	ctx context.Context,
	pool *datamodel.Pool,
	targetCAURI, bootstrapUsername, certificateUsername string,
) error {
	if targetCAURI == "" {
		// Legacy callers that have not enabled CA reconciliation do not carry a
		// desired issuer in activity input. GCP reconciliation always supplies
		// the explicit desired CA URI.
		util.GetLogger(ctx).Debug("No target CA URI supplied; skipping CA reconciliation preflight")
		return nil
	}
	deps := a.dependencies()
	cloudService, err := deps.resolveCloudService(ctx)
	if err != nil {
		return fmt.Errorf("resolve cloud service for CA preflight: %w", err)
	}
	if deps.getHyperscaler() != "gcp" {
		util.GetLogger(ctx).Debug("Skipping GCP CA preflight for non-GCP cloud service")
		return nil
	}
	projectID, poolName, caName, err := parseTargetCAURI(targetCAURI)
	if err != nil {
		return err
	}
	ca, err := cloudService.GetCertificateAuthority(ctx, projectID, env.Region, poolName, caName)
	if err != nil {
		return fmt.Errorf("fetch target certificate authority: %w", err)
	}
	floor, err := certutils.IssuingCALifetimeFloor(
		env.GetString("MINIMUM_ISSUING_CA_REMAINING_LIFETIME", defaultMinimumIssuingCARemainingLifetime),
		env.GetString("CERTIFICATE_LIFETIME", "94608000s"),
	)
	if err != nil {
		return err
	}
	if err := validateCertificateAuthority(ca, deps.now(), floor); err != nil {
		util.GetLogger(ctx).Errorf("Target issuing CA preflight failed: %v", err)
		return err
	}
	chain, err := normalizeCertificateAuthorityChain(ca.PemCertificateChain)
	if err != nil {
		return err
	}
	nodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return fmt.Errorf("get pool nodes for CA preflight: %w", err)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("pool %s has no nodes for CA preflight", pool.UUID)
	}
	provider, err := deps.getProvider(ctx, pool, nodes, env.USERNAME_PWD_SEC_MGR, bootstrapUsername)
	if err != nil {
		return fmt.Errorf("create password-bootstrap provider for CA preflight: %w", err)
	}
	if err := installAndVerifyCertificateAuthorityChain(provider, chain); err != nil {
		return err
	}
	if err := ensureAndVerifyCertificateLogin(provider, certificateUsername); err != nil {
		return err
	}
	return nil
}

func (a *RotateVcpToVsaCertificateActivity) detachStagedPoolCertificate(
	ctx context.Context,
	pool *datamodel.Pool,
) error {
	if pool == nil || pool.PoolCredentials == nil {
		return errors.New("pool credentials are required to detach staged certificate")
	}
	credentials := *pool.PoolCredentials
	expectedCertificateID := credentials.CertificateIDNew
	expectedCAURI := credentials.StagedCaURI
	credentials.CertificateIDNew = ""
	credentials.StagedCaURI = ""
	return a.SE.CompareAndSwapPoolCredentials(
		ctx,
		pool.UUID,
		pool.PoolCredentials.AuthType,
		expectedCertificateID,
		expectedCAURI,
		env.GetCertificateRotationPoolStates(),
		&credentials,
	)
}

func (a *RotateVcpToVsaCertificateActivity) detachAndRevokeStagedPoolCertificate(
	ctx context.Context,
	pool *datamodel.Pool,
	certificateID, caURI string,
	cloudService hyperscaler.Services,
) error {
	if err := a.detachStagedPoolCertificate(ctx, pool); err != nil {
		return err
	}
	pool.PoolCredentials.CertificateIDNew = ""
	pool.PoolCredentials.StagedCaURI = ""
	if caURI == "" {
		return nil
	}
	return a.revokeOldCertificate(ctx, certificateID, caURI, cloudService)
}
