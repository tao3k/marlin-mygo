# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

{
  pkgs,
  ...
}:
{
  packages = [
    pkgs.buf
    pkgs.bun
    pkgs.just
    pkgs.protobuf
    pkgs.protoc-gen-go
    pkgs.protoc-gen-go-grpc
    pkgs.reuse
  ];

  languages.go = {
    enable = true;
  };

  git-hooks.hooks = {
    nixfmt.enable = true;
    agent-defense-g2 = {
      enable = true;
      name = "agent-defense-g2";
      description = "Audit staged production Go files against G2 AI anti-defensive-programming rules";
      entry = toString (
        pkgs.writeShellScript "marlin-mygo-agent-defense-g2" ''
          set -euo pipefail
          CLI="''${DE_AGENT_DEFENSE_AUDIT_CLI:-$HOME/ghq/github.com/hardenedlinux/deterministic-engineering-qnt/packages/agent-defense-audit/src/agent-defense-audit.ts}"
          if [ ! -f "$CLI" ]; then
            echo "SKIP agent-defense-g2: CLI not found at $CLI (set DE_AGENT_DEFENSE_AUDIT_CLI)" >&2
            exit 0
          fi
          exec ${pkgs.bun}/bin/bun "$CLI" "$@"
        ''
      );
      files = "\\.go$";
      excludes = "(^gen/|/tests/|_test\\.go$|^vendor/)";
      pass_filenames = true;
    };
  };
}
