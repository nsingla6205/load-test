# VSCP-6643: control-plane to data-plane encryption

## Scope and dependency

This document records the VCP side of encryption in transit between the
control plane (CP) and the ONTAP/VSA data plane (DP).

VSCP-6643 owns the TLS/identity policy on this hop. The FIPS-validated
cryptographic module used by the shipped VCP binaries is tracked separately by
VSCP-4903. The policy below is necessary independently of that image work, but
the end-to-end FIPS claim requires both tickets to be complete in production.

## Channel inventory

| CP caller | DP endpoint | Protocol and authentication | Server identity |
| --- | --- | --- | --- |
| VCP core ONTAP REST client | ONTAP management API | HTTPS; client certificate (mTLS) for certificate-auth pools, otherwise basic auth inside TLS | Verified against the configured CA/DNS name for certificate-auth pools; legacy basic-auth pools are an exception |
| `ontap-proxy` connection pool | ONTAP management API | HTTPS; mTLS or basic auth inside TLS | Verified for mTLS; legacy basic-auth endpoints are an exception |
| Snapshot private-CLI client | ONTAP `/api/private/cli` | HTTPS; inherits the provider's mTLS/basic-auth configuration | Inherits the provider's verification setting |
| Certificate/password rotation activity | ONTAP management API | HTTPS with authorization header | Legacy management-IP/self-signed-certificate exception |
| VCP SSL bootstrap (`ModifySSL`) | ONTAP management SSH | SSH with password authentication | Uses `x/crypto/ssh` and currently does not verify the host key |
| VLM worker | ONTAP management API | HTTPS/mTLS | Implemented and built in the VLM repository; included here as a system dependency, not changed by this repository |

No plaintext HTTP CP-to-DP client was identified in these paths.

## Enforced HTTPS policy

`utils/tlsconfig.NewFIPSClientConfig` is the shared policy used by the VCP
ONTAP REST client, `ontap-proxy`, the snapshot private-CLI client, and the
rotation activity.

- Minimum protocol: TLS 1.2
- TLS 1.2 key exchange: ephemeral ECDHE
- TLS 1.2 encryption/integrity: AES-128-GCM/SHA-256 or
  AES-256-GCM/SHA-384
- Curves: NIST P-256, P-384, and P-521
- TLS 1.3 remains enabled. Go selects TLS 1.3 cipher suites because its API
  does not permit applications to configure them.
- Production builds compiled with `GOEXPERIMENT=boringcrypto` import
  `crypto/tls/fipsonly`, which prevents TLS from negotiating algorithms outside
  the validated BoringCrypto boundary. This is intentionally activated only
  for BoringCrypto builds so normal developer builds remain supported.
- ChaCha20-Poly1305, static-RSA key exchange, CBC suites, TLS 1.0, and TLS 1.1
  are not offered by the configured TLS 1.2 policy; FIPS-only production builds
  also filter the TLS 1.3 offer to approved suites.

Client-certificate paths load the CA roots and client key pair and perform
normal server-certificate and hostname verification. OCI may connect by IP
while setting an explicit TLS `ServerName`; GCP certificate-auth paths use the
endpoint-to-DNS-name mapping.

## Certificate roles

The phrase "Google-provided certificate" is not precise enough for audit
evidence. The code has three distinct certificate roles:

1. **Issuance and storage (GCP):** VCP requests certificates through the cloud
   provider certificate authority service. Private keys are stored in Secret
   Manager. This is the source of the CP client/server credentials; Google
   infrastructure providing the service is not itself proof that every TLS
   peer certificate is Google-issued.
2. **ONTAP server certificate:** ONTAP presents its configured management
   server certificate. VCP validates its chain and DNS identity on
   certificate-authenticated paths.
3. **VCP client certificate:** VCP presents a client certificate to ONTAP for
   mTLS. ONTAP trusts the corresponding client CA.

The certificate signature algorithm, public-key type and size, issuer, SAN,
validity, and negotiated TLS suite must be captured from a deployed
environment for the final audit packet; source code cannot prove the deployed
certificate contents.

## Residual exceptions and follow-up

The following are explicit residual risks, not evidence of full FIPS
compliance:

- Legacy/basic-auth ONTAP clients set `InsecureSkipVerify=true` because they
  connect to management IPs presenting self-signed certificates. Encryption
  and the TLS policy remain active, but server identity is not authenticated.
- The VCM create-pool path has the same exception until it can use trusted DNS
  or an SNI override.
- The password/certificate rotation helper connects by management IP and
  retains the same verification exception.
- The ONTAP SSH bootstrap uses `ssh.InsecureIgnoreHostKey()` and
  `golang.org/x/crypto/ssh`, which is outside Go BoringCrypto's validated
  boundary. It should be removed in favor of ONTAP REST or given pinned
  host-key verification before claiming that bootstrap path as FIPS compliant.

These exceptions must not be represented as resolved merely because
VSCP-4903 supplies a FIPS-capable container. The preferred remediation is to
issue ONTAP server certificates with stable management DNS SANs and eliminate
`InsecureSkipVerify`; disabling the flag before that identity is available
would break existing pools.

## Evidence note for VSCP-6643

> VCP enforces TLS 1.2 or newer for the identified HTTPS control-plane to
> ONTAP data-plane channels. For TLS 1.2 it offers only ECDHE with AES-GCM
> suites using SHA-256/SHA-384 and NIST P-curves. Certificate-authenticated
> pools use mTLS and validate the ONTAP server certificate against configured
> CA roots and its DNS identity. GCP certificate material is obtained through
> the cloud certificate authority service and private keys are stored in
> Secret Manager. Legacy IP-addressed/basic-auth, VCM, and rotation paths
> still skip ONTAP server-certificate verification and are recorded as
> residual VSCP-6643 risks. The remaining SSH bootstrap path also lacks host
> key verification. FIPS-module validation for the VCP container is a
> dependency tracked by VSCP-4903.

For production close-out, attach a handshake capture or approved equivalent
showing the negotiated protocol/cipher and the peer certificate metadata for
each active path, plus the deployed-image FIPS evidence from VSCP-4903.
