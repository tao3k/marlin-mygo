// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Formal adversarial security model for the marlin-mygo Frontend Engine boundary.
// Models:
//   1. Marketplace release verification (tampered assets cannot activate).
//   2. Slot trust boundaries (restricted slots like approval.gate never mount untrusted plugins).
//   3. Untrusted WebView CallerPage == Host origin pinning & action allowlists.
//   4. Monotonic activation generation pinning across hot-reload races.
//   5. Circuit breaker fault isolation with atomic native fallback restoration.
//   6. Mutating native device capabilities (iOS/Android) requiring Rust one-shot GateReceipt + OS permission.
//
// Includes 7 explicit adversarial negative-control step actions (badStep*) — one
// per security invariant — that MUST violate `safe` when checked by
// `marlin-mygo-tool model-gate`.
module frontendPluginSecurity {
  val plugins = Set("pA", "pB", "pEvil")
  val slots = Set("workbench.main", "approval.gate")
  val actions = Set("org.move", "diff.view", "shell.exec")
  val topics = Set("org.tasks", "workspace.diff", "host.secrets")

  pure def declaredActions(p: str): Set[str] =
    if (p == "pA") Set("org.move")
    else if (p == "pB") Set("diff.view")
    else Set()

  pure def readScopes(p: str): Set[str] =
    if (p == "pA") Set("org.tasks")
    else if (p == "pB") Set("workspace.diff")
    else Set()

  pure def slotFallback(sl: str): str =
    if (sl == "workbench.main") "native.main" else "native.gate"

  pure def slotAllowsUntrusted(sl: str): bool =
    sl == "workbench.main"

  type ActionRecord = { plugin: str, caller: str, gen: int, act: str }
  type EventRecord = { plugin: str, gen: int, topic: str }
  type DeviceWriteRecord = { gateId: int, hasReceipt: bool, osPermitted: bool }

  type State = {
    verified: str -> bool,
    tampered: str -> bool,
    gen: str -> int,
    breakerOpen: str -> bool,
    slotOwner: str -> str,
    slotTrusted: str -> bool,
    gateId: int,
    gateState: str, // "none" | "pending" | "approved" | "rejected" | "claimed"
    admittedActions: List[ActionRecord],
    deliveredEvents: List[EventRecord],
    deviceWrites: List[DeviceWriteRecord],
  }

  var s: State

  action init = s' = {
    verified: Map("pA" -> false, "pB" -> false, "pEvil" -> false),
    tampered: Map("pA" -> false, "pB" -> false, "pEvil" -> true),
    gen: Map("pA" -> 0, "pB" -> 0, "pEvil" -> 0),
    breakerOpen: Map("pA" -> false, "pB" -> false, "pEvil" -> false),
    slotOwner: Map("workbench.main" -> "native.main", "approval.gate" -> "native.gate"),
    slotTrusted: Map("workbench.main" -> true, "approval.gate" -> true),
    gateId: 0,
    gateState: "none",
    admittedActions: [],
    deliveredEvents: [],
    deviceWrites: [],
  }

  // Marketplace Ed25519 + SHA-256 staging gate: tampered bundles are never verified.
  action stageRelease(p: str, assetTampered: bool): bool = all {
    plugins.contains(p),
    s.gen.get(p) == 0,
    s' = if (assetTampered or p == "pEvil")
      s.with("tampered", s.tampered.set(p, true)).with("verified", s.verified.set(p, false))
    else
      s.with("tampered", s.tampered.set(p, false)).with("verified", s.verified.set(p, true)),
  }

  action stageAnyRelease = {
    nondet p = plugins.oneOf()
    nondet bad = Set(true, false).oneOf()
    stageRelease(p, bad)
  }

  // Activate a verified plugin into a slot that permits untrusted renderers.
  action activate(p: str, sl: str): bool = all {
    plugins.contains(p),
    slots.contains(sl),
    s.verified.get(p),
    not(s.tampered.get(p)),
    slotAllowsUntrusted(sl),
    s.gen.get(p) < 4,
    s' = s
      .with("gen", s.gen.set(p, s.gen.get(p) + 1))
      .with("breakerOpen", s.breakerOpen.set(p, false))
      .with("slotOwner", s.slotOwner.set(sl, p))
      .with("slotTrusted", s.slotTrusted.set(sl, false)),
  }

  action activateAny = {
    nondet p = plugins.oneOf()
    nondet sl = slots.oneOf()
    activate(p, sl)
  }

  // Hot-reload bumps generation atomically while keeping verified status.
  action hotReload(p: str): bool = all {
    plugins.contains(p),
    s.gen.get(p) > 0,
    s.gen.get(p) < 4,
    s' = s
      .with("gen", s.gen.set(p, s.gen.get(p) + 1))
      .with("breakerOpen", s.breakerOpen.set(p, false)),
  }

  action hotReloadAny = {
    nondet p = plugins.oneOf()
    hotReload(p)
  }

  // Fault/circuit-breaker trip or unmount: atomically restores native fallback on any owned slot.
  action tripBreakerOrUnmount(p: str): bool = all {
    plugins.contains(p),
    s.gen.get(p) > 0,
    val nextOwners = slots.mapBy(sl =>
      if (s.slotOwner.get(sl) == p) slotFallback(sl) else s.slotOwner.get(sl)
    )
    val nextTrusted = slots.mapBy(sl =>
      if (s.slotOwner.get(sl) == p) true else s.slotTrusted.get(sl)
    )
    s' = s
      .with("breakerOpen", s.breakerOpen.set(p, true))
      .with("slotOwner", nextOwners)
      .with("slotTrusted", nextTrusted),
  }

  action tripAnyBreaker = {
    nondet p = plugins.oneOf()
    tripBreakerOrUnmount(p)
  }

  // Scoped action ingress: enforces CallerPage == Host, live generation, declared allowlist, closed breaker.
  action submitAction(caller: str, host: str, act: str, reqGen: int): bool = all {
    plugins.contains(caller),
    plugins.contains(host),
    actions.contains(act),
    s.admittedActions.length() < 6,
    val allowed = all {
      caller == host,
      s.gen.get(caller) > 0,
      reqGen == s.gen.get(caller),
      not(s.breakerOpen.get(caller)),
      declaredActions(caller).contains(act),
    }
    s' = if (allowed)
      s.with("admittedActions", s.admittedActions.append({ plugin: host, caller: caller, gen: reqGen, act: act }))
    else s,
  }

  action submitAnyAction = {
    nondet caller = plugins.oneOf()
    nondet host = plugins.oneOf()
    nondet act = actions.oneOf()
    nondet g = Set(0, 1, 2, 3).oneOf()
    submitAction(caller, host, act, g)
  }

  // Scoped event routing: enforces live generation, closed breaker, and read_scopes allowlist.
  action routeEvent(p: str, topic: str, evGen: int): bool = all {
    plugins.contains(p),
    topics.contains(topic),
    s.deliveredEvents.length() < 6,
    val allowed = all {
      s.gen.get(p) > 0,
      evGen == s.gen.get(p),
      not(s.breakerOpen.get(p)),
      readScopes(p).contains(topic),
    }
    s' = if (allowed)
      s.with("deliveredEvents", s.deliveredEvents.append({ plugin: p, gen: evGen, topic: topic }))
    else s,
  }

  action routeAnyEvent = {
    nondet p = plugins.oneOf()
    nondet topic = topics.oneOf()
    nondet g = Set(0, 1, 2, 3).oneOf()
    routeEvent(p, topic, g)
  }

  // Device capability gate lifecycle (marlin.gate.v1 + marlin.device.v1).
  action openDeviceGate = all {
    Set("none", "rejected", "claimed").contains(s.gateState),
    s.gateId < 4,
    s' = s.with("gateId", s.gateId + 1).with("gateState", "pending"),
  }

  action resolveDeviceGate(approve: bool): bool = all {
    s.gateState == "pending",
    s' = s.with("gateState", if (approve) "approved" else "rejected"),
  }

  action resolveAnyGate = {
    nondet ok = Set(true, false).oneOf()
    resolveDeviceGate(ok)
  }

  // Mutating device write consumes one-shot approval claim and requires OS permission.
  action invokeDeviceWrite(osPermitted: bool): bool = all {
    s.deviceWrites.length() < 6,
    val allowed = s.gateState == "approved" and osPermitted
    s' = if (allowed)
      s.with("gateState", "claimed")
       .with("deviceWrites", s.deviceWrites.append({ gateId: s.gateId, hasReceipt: true, osPermitted: true }))
    else s,
  }

  action invokeAnyDeviceWrite = {
    nondet osOk = Set(true, false).oneOf()
    invokeDeviceWrite(osOk)
  }

  action step = any {
    stageAnyRelease,
    activateAny,
    hotReloadAny,
    tripAnyBreaker,
    submitAnyAction,
    routeAnyEvent,
    openDeviceGate,
    resolveAnyGate,
    invokeAnyDeviceWrite,
  }

  // --- Adversarial negative controls (each deliberately breaks one security gate) ---

  // Control 1: forgets CallerPage == Host check in marlin-plugin:// handler.
  action badStepOriginSpoof = any {
    step,
    all {
      s.gen.get("pA") > 0,
      s' = s.with("admittedActions", s.admittedActions.append({
        plugin: "pA", caller: "pEvil", gen: s.gen.get("pA"), act: "org.move",
      })),
    },
  }

  // Control 2: mounts an untrusted plugin into restricted approval.gate slot.
  action badStepRestrictedSlotHijack = any {
    step,
    all {
      s.verified.get("pA"),
      s' = s
        .with("slotOwner", s.slotOwner.set("approval.gate", "pA"))
        .with("slotTrusted", s.slotTrusted.set("approval.gate", false)),
    },
  }

  // Control 3: executes mutating iOS/Android device write without Rust GateReceipt.
  action badStepDeviceWriteNoReceipt = any {
    step,
    all {
      s.gateState != "approved",
      s' = s.with("deviceWrites", s.deviceWrites.append({
        gateId: s.gateId, hasReceipt: false, osPermitted: true,
      })),
    },
  }

  // Control 4: trips circuit breaker but forgets to restore native slot fallback.
  action badStepBreakerForgetsFallback = any {
    step,
    all {
      s.slotOwner.get("workbench.main") == "pA",
      s' = s.with("breakerOpen", s.breakerOpen.set("pA", true)),
    },
  }

  // Control 5: activates a tampered/unverified bundle directly.
  action badStepTamperedPluginActive = any {
    step,
    all {
      s.gen.get("pEvil") == 0,
      s' = s.with("gen", s.gen.set("pEvil", 1)),
    },
  }

  // Control 6: leaks host.secrets topic to an untrusted plugin outside readScopes.
  action badStepSecretTopicLeak = any {
    step,
    all {
      s.gen.get("pA") > 0,
      s' = s.with("deliveredEvents", s.deliveredEvents.append({
        plugin: "pA", gen: s.gen.get("pA"), topic: "host.secrets",
      })),
    },
  }

  // Control 7: replays an already-claimed one-shot Rust GateReceipt for a second device write.
  action badStepDeviceWriteReplay = any {
    step,
    all {
      s.gateState == "claimed",
      s.gateId > 0,
      s' = s.with("deviceWrites", s.deviceWrites.append({
        gateId: s.gateId, hasReceipt: true, osPermitted: true,
      })),
    },
  }

  // --- Formal Safety Invariants ---

  val invRestrictedSlotAlwaysTrusted =
    s.slotOwner.get("approval.gate") == "native.gate" and s.slotTrusted.get("approval.gate")

  val invNoTamperedPluginActive =
    plugins.forall(p => s.gen.get(p) > 0 implies (s.verified.get(p) and not(s.tampered.get(p))))

  val invNoCrossOriginOrUndeclaredAction =
    s.admittedActions.foldl(true, (acc, r) => acc and all {
      r.caller == r.plugin,
      r.gen > 0,
      declaredActions(r.plugin).contains(r.act),
    })

  val invNoUnauthorizedTopicDelivered =
    s.deliveredEvents.foldl(true, (acc, e) => acc and all {
      e.gen > 0,
      readScopes(e.plugin).contains(e.topic),
      e.topic != "host.secrets",
    })

  val invDeviceWritesHaveReceiptAndUniqueClaim = {
    val allAuth = s.deviceWrites.foldl(true, (acc, w) => acc and w.hasReceipt and w.osPermitted and w.gateId > 0)
    val uniqueIds = s.deviceWrites.foldl(Set(), (ids, w) => ids.union(Set(w.gateId)))
    allAuth and (uniqueIds.size() == s.deviceWrites.length())
  }

  val invOpenBreakerOwnsNoSlot =
    plugins.forall(p =>
      s.breakerOpen.get(p) implies slots.forall(sl => s.slotOwner.get(sl) != p)
    )

  val safe = all {
    invRestrictedSlotAlwaysTrusted,
    invNoTamperedPluginActive,
    invNoCrossOriginOrUndeclaredAction,
    invNoUnauthorizedTopicDelivered,
    invDeviceWritesHaveReceiptAndUniqueClaim,
    invOpenBreakerOwnsNoSlot,
  }

  // --- Executable Quint Tests ---

  run happyPathAndFallbackTest = {
    init
      .then(stageRelease("pA", false))
      .then(activate("pA", "workbench.main"))
      .then(submitAction("pA", "pA", "org.move", 1))
      .then(submitAction("pEvil", "pA", "org.move", 1))   // spoof blocked
      .then(submitAction("pA", "pA", "shell.exec", 1))    // undeclared blocked
      .then(routeEvent("pA", "host.secrets", 1))          // secret topic blocked
      .then(routeEvent("pA", "org.tasks", 1))             // allowed topic delivered
      .then(tripBreakerOrUnmount("pA"))
      .then(all {
        assert(safe),
        assert(s.admittedActions.length() == 1),
        assert(s.deliveredEvents.length() == 1),
        assert(s.slotOwner.get("workbench.main") == "native.main"),
        s' = s,
      })
  }

  run oneShotDeviceGateTest = {
    init
      .then(openDeviceGate)
      .then(invokeDeviceWrite(true))   // pending -> refused
      .then(resolveDeviceGate(true))   // approved
      .then(invokeDeviceWrite(true))   // claimed -> executed once
      .then(invokeDeviceWrite(true))   // replay after claim -> refused
      .then(all {
        assert(safe),
        assert(s.deviceWrites.length() == 1),
        assert(s.gateState == "claimed"),
        s' = s,
      })
  }

  run hotReloadRejectsStaleGenTest = {
    init
      .then(stageRelease("pA", false))
      .then(activate("pA", "workbench.main"))
      .then(hotReload("pA"))
      .then(submitAction("pA", "pA", "org.move", 1)) // stale gen 1 -> rejected
      .then(routeEvent("pA", "org.tasks", 1))        // stale gen 1 -> rejected
      .then(submitAction("pA", "pA", "org.move", 2)) // live gen 2 -> admitted
      .then(all {
        assert(safe),
        assert(s.admittedActions.length() == 1),
        assert(s.deliveredEvents.length() == 0),
        s' = s,
      })
  }
}
