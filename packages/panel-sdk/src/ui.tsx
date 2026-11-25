// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Reusable, theme-reactive React 19 UI primitives built strictly on
 * `@tao3k/marlin-panel-sdk` derived `--marlin-*` tokens. Eliminates duplicated
 * plugin CSS and ugly native HTML `<select>` controls across every WebView panel.
 */

import React, { memo, type ReactNode } from "react";

export type SemanticTone = "accent" | "info" | "success" | "warning" | "danger" | "muted";

export function PanelShell({ children }: { children: ReactNode }) {
  return <div className="m-panel">{children}</div>;
}

export const PanelHeader = memo(function PanelHeader({
  title,
  caption,
  actions,
}: {
  title: string;
  caption?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="m-header">
      <div>
        <h1 className="m-title">{title}</h1>
        {caption && <div className="m-caption">{caption}</div>}
      </div>
      {actions && <div className="m-row-gap">{actions}</div>}
    </header>
  );
});

export interface SegmentedOption<T extends string> {
  key: T;
  label: string;
}

function SegmentedControlImpl<T extends string>({
  options,
  value,
  onChange,
}: {
  options: readonly SegmentedOption<T>[];
  value: T;
  onChange: (next: T) => void;
}) {
  return (
    <div className="m-segmented" role="tablist">
      {options.map((opt) => (
        <button
          key={opt.key}
          type="button"
          role="tab"
          aria-selected={value === opt.key}
          className={`m-btn ${value === opt.key ? "active" : ""}`}
          onClick={() => onChange(opt.key)}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

export const SegmentedControl = memo(SegmentedControlImpl) as typeof SegmentedControlImpl;

export interface StatItem {
  label: string;
  value: string | number;
}

export const StatStrip = memo(function StatStrip({ items }: { items: readonly StatItem[] }) {
  return (
    <div className="m-stat-grid">
      {items.map((item) => (
        <div className="m-stat" key={item.label}>
          <span className="m-stat-value">{item.value}</span>
          <span className="m-stat-label">{item.label}</span>
        </div>
      ))}
    </div>
  );
});

export function SectionCard({
  title,
  right,
  children,
}: {
  title: string;
  right?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="m-card">
      <div className="m-card-head">
        <h3 className="m-section-label">{title}</h3>
        {right}
      </div>
      {children}
    </section>
  );
}

export function StatusBadge({
  tone = "muted",
  children,
}: {
  tone?: SemanticTone;
  children: ReactNode;
}) {
  return <span className={`m-badge tone-${tone}`}>{children}</span>;
}

export function ToastBanner({
  message,
  onDismiss,
}: {
  message: string;
  onDismiss?: () => void;
}) {
  if (!message) return null;
  return (
    <div className="m-toast" role="status">
      <span>{message}</span>
      {onDismiss && (
        <button type="button" className="m-btn m-btn-ghost" onClick={onDismiss}>
          Dismiss
        </button>
      )}
    </div>
  );
}
