// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Marlin Tier-0 AuthManager threat-first scenario models consuming
// @deterministic-engineering/quint-models via bare package specifiers.
// Maps directly to control/auth/{manager.go,oidc.go,oidc_discovery.go,pkce.go,loopback_native.go,vault.go,keychain.go,authorizer.go}.
module marlin_auth_threat {
  import oauth_token_lifecycle.*
    from "@deterministic-engineering/quint-models/threat/identity/oauth-token-lifecycle/OAuthTokenLifecycle"
}

module marlin_oidc_threat {
  import oidc_federation.*
    from "@deterministic-engineering/quint-models/threat/identity/oidc-federation/OidcFederation"
}

module marlin_device_threat {
  import device_authorization.*
    from "@deterministic-engineering/quint-models/threat/identity/device-authorization/DeviceAuthorization"
}
