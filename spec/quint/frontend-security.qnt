// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Formal adversarial security model for the marlin-mygo Frontend Engine & Plugin Ecosystem.
// Models 10 formal safety invariants:
//   1. Restricted slot trust (approval.gate never mounts untrusted plugins).
//   2. Marketplace Ed25519 release verification (tampered/rogue bundles cannot activate).
//   3. Untrusted WebView CallerPage == Host origin pinning & action allowlists.
//   4. Scoped read_scopes enforcement (host.secrets never delivered to untrusted plugins).
//   5. Mutating platform device writes require one-shot Rust GateReceipt + OS permission.
//   6. Circuit breaker fault isolation with atomic host fallback restoration.
//   7. Lockfile pinned_sha256 integrity on lazy wake (pin mismatch never activates).
//   8. Emacs-style lazy Dormant/Sleep/ReapIdle isolation (dormant plugins own no slot).
//   9. Bounded O(1) LRU working-set cap (evictable active count <= maxActiveLazy).
//  10. Anti-silent upgrade privilege escalation (added permissions require user confirmation).
//
// Includes 11 explicit adversarial negative-control step actions (badStep*) — one
// per threat vector — that MUST violate `safe` when checked by `marlin-mygo-tool model-gate`.
module frontendPluginSecurity {
  val plugins = Set("pA", "pB", "pEvil")
  val slots = Set("workbench.main", "workbench.inspector", "approval.gate")
  val actions = Set("org.move", "diff.view", "shell.exec")
  val topics = Set("org.tasks", "workspace.diff", "host.secrets")
  val maxActiveLazy = 1

  pure def declaredActions(p: str): Set[str] =
    if (p == "pA") Set("org.move")
    else if (p == "pB") Set("diff.view")
    else Set()

  pure def readScopes(p: str): Set[str] =
    if (p == "pA") Set("org.tasks")
    else if (p == "pB") Set("workspace.diff")
    else Set()

  pure def isEvictableLazy(p: str): bool =
    p == "pA" or p == "pB"

  pure def slotFallback(sl: str): str =
    if (sl == "workbench.main") "host.main"
    else if (sl == "workbench.inspector") "host.inspector"
    else "host.gate"

  pure def slotAllowsUntrusted(sl: str): bool =
    sl == "workbench.main" or sl == "workbench.inspector"

  type ActionRecord = { plugin: str, caller: str, gen: int, act: str }
  type EventRecord = { plugin: str, gen: int, topic: str }
  type DeviceWriteRecord = { gateId: int, hasReceipt: bool, osPermitted: bool }

  type State = {
    verified: str -> bool,
    tampered: str -> bool,
    pinMatch: str -> bool,
    dormant: str -> bool,
    gen: str -> int,
    breakerOpen: str -> bool,
    slotOwner: str -> str,
    slotTrusted: str -> bool,
    pendingEscalation: str -> bool,
    escalationConfirmed: str -> bool,
    escalatedActive: str -> bool,
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
    pinMatch: Map("pA" -> false, "pB" -> false, "pEvil" -> false),
    dormant: Map("pA" -> true, "pB" -> true, "pEvil" -> true),
    gen: Map("pA" -> 0, "pB" -> 0, "pEvil" -> 0),
    breakerOpen: Map("pA" -> false, "pB" -> false, "pEvil" -> false),
    slotOwner: Map(
      "workbench.main" -> "host.main",
      "workbench.inspector" -> "host.inspector",
      "approval.gate" -> "host.gate"
    ),
    slotTrusted: Map(
      "workbench.main" -> true,
      "workbench.inspector" -> true,
      "approval.gate" -> true
    ),
    pendingEscalation: Map("pA" -> false, "pB" -> false, "pEvil" -> false),
    escalationConfirmed: Map("pA" -> false, "pB" -> false, "pEvil" -> false),
    escalatedActive: Map("pA" -> false, "pB" -> false, "pEvil" -> false),
    gateId: 0,
    gateState: "none",
    admittedActions: [],
    deliveredEvents: [],
    deviceWrites: [],
  }

  // Marketplace Ed25519 + SHA-256 staging gate: tampered/rogue bundles are never verified.
  action stageRelease(p: str, assetTampered: bool): bool = all {
    plugins.contains(p),
    s.dormant.get(p),
    s' = if (assetTampered or p == "pEvil")
      s.with("tampered", s.tampered.set(p, true))
       .with("verified", s.verified.set(p, false))
       .with("pinMatch", s.pinMatch.set(p, false))
    else
      s.with("tampered", s.tampered.set(p, false))
       .with("verified", s.verified.set(p, true))
       .with("pinMatch", s.pinMatch.set(p, true)),
  }

  action stageAnyRelease = {
    nondet p = plugins.oneOf()
    nondet bad = Set(true, false).oneOf()
    stageRelease(p, bad)
  }

  // Adversarial supply-chain drift: remote bundle changes so lockfile pin no longer matches.
  action driftLockfilePin(p: str): bool = all {
    plugins.contains(p),
    s.dormant.get(p),
    s' = s.with("pinMatch", s.pinMatch.set(p, false)),
  }

  action driftAnyPin = {
    nondet p = plugins.oneOf()
    driftLockfilePin(p)
  }

  // Activate or lazily wake a verified, pin-matched plugin into a non-restricted slot.
  // Enforces bounded LRU working-set cap (evicting other active evictable plugins atomically).
  action activate(p: str, sl: str): bool = all {
    plugins.contains(p),
    slots.contains(sl),
    s.verified.get(p),
    not(s.tampered.get(p)),
    s.pinMatch.get(p),
    slotAllowsUntrusted(sl),
    s.gen.get(p) < 4,
    val evicted = plugins.filter(q =>
      q != p and isEvictableLazy(q) and not(s.dormant.get(q))
    )
    val nextDormant = plugins.mapBy(q =>
      if (q == p) false
      else if (evicted.contains(q)) true
      else s.dormant.get(q)
    )
    val nextOwners = slots.mapBy(curSl =>
      if (curSl == sl) p
      else if (evicted.contains(s.slotOwner.get(curSl))) slotFallback(curSl)
      else s.slotOwner.get(curSl)
    )
    val nextTrusted = slots.mapBy(curSl =>
      if (curSl == sl) false
      else if (evicted.contains(s.slotOwner.get(curSl))) true
      else s.slotTrusted.get(curSl)
    )
    s' = s
      .with("gen", s.gen.set(p, s.gen.get(p) + 1))
      .with("dormant", nextDormant)
      .with("breakerOpen", s.breakerOpen.set(p, false))
      .with("slotOwner", nextOwners)
      .with("slotTrusted", nextTrusted),
  }

  action activateAny = {
    nondet p = plugins.oneOf()
    nondet sl = slots.oneOf()
    activate(p, sl)
  }

  // Hot-reload bumps generation atomically while active and pin-verified.
  action hotReload(p: str): bool = all {
    plugins.contains(p),
    not(s.dormant.get(p)),
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

  // Emacs-style Sleep / O(1) ReapIdle: unloads plugin to dormant (0MB RAM) and restores host fallback.
  action sleepOrReap(p: str): bool = all {
    plugins.contains(p),
    not(s.dormant.get(p)),
    val nextOwners = slots.mapBy(sl =>
      if (s.slotOwner.get(sl) == p) slotFallback(sl) else s.slotOwner.get(sl)
    )
    val nextTrusted = slots.mapBy(sl =>
      if (s.slotOwner.get(sl) == p) true else s.slotTrusted.get(sl)
    )
    s' = s
      .with("dormant", s.dormant.set(p, true))
      .with("slotOwner", nextOwners)
      .with("slotTrusted", nextTrusted),
  }

  action sleepAny = {
    nondet p = plugins.oneOf()
    sleepOrReap(p)
  }

  // Fault/circuit-breaker trip or unmount: atomically restores host fallback on any owned slot.
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
      .with("dormant", s.dormant.set(p, true))
      .with("slotOwner", nextOwners)
      .with("slotTrusted", nextTrusted),
  }

  action tripAnyBreaker = {
    nondet p = plugins.oneOf()
    tripBreakerOrUnmount(p)
  }

  // Upgrade privilege escalation two-step confirmation protocol.
  action proposeEscalatingUpgrade(p: str): bool = all {
    plugins.contains(p),
    not(s.dormant.get(p)),
    s.verified.get(p),
    not(s.pendingEscalation.get(p)),
    not(s.escalatedActive.get(p)),
    s' = s
      .with("pendingEscalation", s.pendingEscalation.set(p, true))
      .with("escalationConfirmed", s.escalationConfirmed.set(p, false)),
  }

  action proposeAnyEscalation = {
    nondet p = plugins.oneOf()
    proposeEscalatingUpgrade(p)
  }

  action confirmEscalation(p: str): bool = all {
    plugins.contains(p),
    s.pendingEscalation.get(p),
    s' = s.with("escalationConfirmed", s.escalationConfirmed.set(p, true)),
  }

  action confirmAnyEscalation = {
    nondet p = plugins.oneOf()
    confirmEscalation(p)
  }

  action applyEscalatedUpgrade(p: str): bool = all {
    plugins.contains(p),
    not(s.dormant.get(p)),
    s.pendingEscalation.get(p),
    s.escalationConfirmed.get(p),
    s.gen.get(p) < 4,
    s' = s
      .with("pendingEscalation", s.pendingEscalation.set(p, false))
      .with("escalatedActive", s.escalatedActive.set(p, true))
      .with("gen", s.gen.set(p, s.gen.get(p) + 1)),
  }

  action applyAnyEscalatedUpgrade = {
    nondet p = plugins.oneOf()
    applyEscalatedUpgrade(p)
  }

  // Scoped action ingress: enforces CallerPage == Host, active (non-dormant), live generation, allowlist, closed breaker.
  action submitAction(caller: str, host: str, act: str, reqGen: int): bool = all {
    plugins.contains(caller),
    plugins.contains(host),
    actions.contains(act),
    s.admittedActions.length() < 6,
    val allowed = all {
      caller == host,
      not(s.dormant.get(caller)),
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

  // Scoped event routing: enforces active (non-dormant), live generation, closed breaker, and read_scopes allowlist.
  action routeEvent(p: str, topic: str, evGen: int): bool = all {
    plugins.contains(p),
    topics.contains(topic),
    s.deliveredEvents.length() < 6,
    val allowed = all {
      not(s.dormant.get(p)),
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
    driftAnyPin,
    activateAny,
    hotReloadAny,
    sleepAny,
    tripAnyBreaker,
    proposeAnyEscalation,
    confirmAnyEscalation,
    applyAnyEscalatedUpgrade,
    submitAnyAction,
    routeAnyEvent,
    openDeviceGate,
    resolveAnyGate,
    invokeAnyDeviceWrite,
  }

  // --- 11 Adversarial Negative Controls (each deliberately breaks ONE security gate) ---

  // Control 1: forgets CallerPage == Host check in marlin-plugin:// handler.
  action badStepOriginSpoof = any {
    step,
    all {
      not(s.dormant.get("pA")),
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

  // Control 4: trips circuit breaker but forgets to restore host slot fallback.
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
      s' = s
        .with("gen", s.gen.set("pEvil", 1))
        .with("dormant", s.dormant.set("pEvil", false)),
    },
  }

  // Control 6: leaks host.secrets topic to an untrusted plugin outside readScopes.
  action badStepSecretTopicLeak = any {
    step,
    all {
      not(s.dormant.get("pA")),
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

  // Control 8: LazyLoader wakes a verified plugin whose bundle digest drifted from lockfile pinned_sha256.
  action badStepPinMismatchWake = any {
    step,
    all {
      s.verified.get("pA"),
      s.dormant.get("pA"),
      not(s.pinMatch.get("pA")),
      s' = s
        .with("dormant", s.dormant.set("pA", false))
        .with("gen", s.gen.set("pA", s.gen.get("pA") + 1)),
    },
  }

  // Control 9: Sleep / ReapIdle marks a lazy plugin dormant but forgets to restore slot host.* fallback.
  action badStepDormantForgetsFallback = any {
    step,
    all {
      s.slotOwner.get("workbench.main") == "pA",
      not(s.dormant.get("pA")),
      s' = s.with("dormant", s.dormant.set("pA", true)),
    },
  }

  // Control 10: Wakes a second evictable lazy plugin without evicting LRU victim (breaks MaxActiveLazy cap).
  action badStepLRUCapExceeded = any {
    step,
    all {
      s.verified.get("pA"),
      s.verified.get("pB"),
      s.pinMatch.get("pA"),
      s.pinMatch.get("pB"),
      not(s.dormant.get("pA")),
      s.dormant.get("pB"),
      s' = s
        .with("dormant", s.dormant.set("pB", false))
        .with("gen", s.gen.set("pB", s.gen.get("pB") + 1))
        .with("slotOwner", s.slotOwner.set("workbench.inspector", "pB"))
        .with("slotTrusted", s.slotTrusted.set("workbench.inspector", false)),
    },
  }

  // Control 11: Silently installs an upgrade that adds new permissions without explicit user confirmation.
  action badStepSilentPrivilegeEscalation = any {
    step,
    all {
      s.pendingEscalation.get("pA"),
      not(s.escalationConfirmed.get("pA")),
      s' = s
        .with("pendingEscalation", s.pendingEscalation.set("pA", false))
        .with("escalatedActive", s.escalatedActive.set("pA", true)),
    },
  }

  // --- 10 Formal Safety Invariants ---

  val invRestrictedSlotAlwaysTrusted =
    s.slotOwner.get("approval.gate") == "host.gate" and s.slotTrusted.get("approval.gate")

  val invNoTamperedPluginActive =
    plugins.forall(p => not(s.dormant.get(p)) implies (s.gen.get(p) > 0 and s.verified.get(p) and not(s.tampered.get(p))))

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

  val invPinMismatchNeverActive =
    plugins.forall(p => not(s.dormant.get(p)) implies s.pinMatch.get(p))

  val invDormantOwnsNoSlot =
    plugins.forall(p =>
      s.dormant.get(p) implies slots.forall(sl => s.slotOwner.get(sl) != p)
    )

  val invWorkingSetBoundedByLRUCap =
    plugins.filter(p => isEvictableLazy(p) and not(s.dormant.get(p))).size() <= maxActiveLazy

  val invNoUnconfirmedEscalationUpgrade =
    plugins.forall(p => s.escalatedActive.get(p) implies s.escalationConfirmed.get(p))

  val safe = all {
    invRestrictedSlotAlwaysTrusted,
    invNoTamperedPluginActive,
    invNoCrossOriginOrUndeclaredAction,
    invNoUnauthorizedTopicDelivered,
    invDeviceWritesHaveReceiptAndUniqueClaim,
    invOpenBreakerOwnsNoSlot,
    invPinMismatchNeverActive,
    invDormantOwnsNoSlot,
    invWorkingSetBoundedByLRUCap,
    invNoUnconfirmedEscalationUpgrade,
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
        assert(s.slotOwner.get("workbench.main") == "host.main"),
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

  run lazyPinWakeSleepAndLRUEvictionTest = {
    init
      .then(stageRelease("pA", false))
      .then(stageRelease("pB", false))
      .then(activate("pA", "workbench.main"))
      .then(activate("pB", "workbench.inspector")) // evicts pA back to dormant + restores host.main!
      .then(submitAction("pA", "pA", "org.move", 1)) // dormant evicted pA -> rejected
      .then(sleepOrReap("pB"))                       // idle reap pB -> restores host.inspector
      .then(all {
        assert(safe),
        assert(s.dormant.get("pA")),
        assert(s.dormant.get("pB")),
        assert(s.slotOwner.get("workbench.main") == "host.main"),
        assert(s.slotOwner.get("workbench.inspector") == "host.inspector"),
        assert(s.admittedActions.length() == 0),
        s' = s,
      })
  }

  run upgradePrivilegeEscalationConfirmationTest = {
    init
      .then(stageRelease("pA", false))
      .then(activate("pA", "workbench.main"))
      .then(proposeEscalatingUpgrade("pA"))
      .then(confirmEscalation("pA"))
      .then(applyEscalatedUpgrade("pA"))
      .then(all {
        assert(safe),
        assert(s.escalatedActive.get("pA")),
        assert(s.escalationConfirmed.get("pA")),
        assert(s.gen.get("pA") == 2),
        s' = s,
      })
  }
}
