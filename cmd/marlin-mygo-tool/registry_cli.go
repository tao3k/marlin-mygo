// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/tao3k/marlin-mygo/control/marketplace"
)

func runValidateRegistry(args []string) error {
	fs := flag.NewFlagSet("validate-registry", flag.ContinueOnError)
	filePath := fs.String("file", "marketplace/registry.json", "path to Official Marketplace registry.json")
	repoRoot := fs.String("root", ".", "repository root for verifying in-tree local_path manifests")
	if err := fs.Parse(args); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(*repoRoot)
	if err != nil {
		return err
	}
	m, err := marketplace.LoadRegistryManifest(*filePath)
	if err != nil {
		return err
	}
	if err := marketplace.ValidateRegistryManifest(m, absRoot); err != nil {
		return err
	}
	fmt.Printf("registry valid: %s (%d plugins, %d themes, %d layouts, policy=%s)\n",
		m.MarketplaceID, len(m.Plugins), len(m.Themes), len(m.Layouts), m.SigningPolicy)
	return nil
}
