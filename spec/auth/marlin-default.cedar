// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Marlin downstream-specific Cedar authorization policy set.
// Consumes host-owned context injected by AuthManager (email_verified, scopes, epochs).

@id("permit-grpc-invoke-with-openid")
permit (
    principal is Marlin::Account,
    action == Marlin::Action::"invoke_grpc",
    resource is Marlin::Resource
)
when {
    context.scopes.contains("openid")
};

@id("permit-install-plugin-with-verified-scope")
permit (
    principal is Marlin::Account,
    action == Marlin::Action::"install_plugin",
    resource is Marlin::Resource
)
when {
    context.email_verified &&
    context.scopes.contains("plugin:install")
};

@id("permit-manage-vault-with-verified-admin")
permit (
    principal is Marlin::Account,
    action == Marlin::Action::"manage_vault",
    resource is Marlin::Resource
)
when {
    context.email_verified &&
    context.scopes.contains("vault:admin")
};

@id("forbid-privileged-actions-unverified-email")
forbid (
    principal is Marlin::Account,
    action in [
        Marlin::Action::"install_plugin",
        Marlin::Action::"manage_vault",
        Marlin::Action::"access_device"
    ],
    resource is Marlin::Resource
)
when {
    !context.email_verified
};

@id("forbid-cross-tenant-access")
forbid (
    principal is Marlin::Account,
    action,
    resource is Marlin::Resource
)
when {
    context has resource_tenant_id &&
    context.resource_tenant_id != "" &&
    (!(context has tenant_id) || context.tenant_id != context.resource_tenant_id)
};

@id("forbid-delegated-agent-human-gate")
forbid (
    principal is Marlin::Account,
    action == Marlin::Action::"approve_gate",
    resource is Marlin::Resource
)
when {
    context has delegated &&
    context.delegated
};
