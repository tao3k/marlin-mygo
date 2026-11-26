// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/protocol/chatroom-ingress-governance/ChatroomIngressGovernance"

// Marlin downstream-specific Agent Chat Room overlay resolved transparently via
// quint-modules-resolver. Extends ChatroomIngressGovernance with tenant-pinned
// room isolation and Tier-0 PromptGate idempotency key tracking.
module marlin_chatroom_composed {
  type State = { roomTenant: int, claimedKeys: int, crossTenantRoomLeak: bool }
  action init = h' = { roomTenant: 1, claimedKeys: 0, crossTenantRoomLeak: false }
  action ingestTenantRoomEventAs(evTenant: int): bool = if (evTenant == h.roomTenant)
    h' = h.with("taintedExternalMsg", true).with("crossTenantRoomLeak", h.crossTenantRoomLeak or evTenant != h.roomTenant)
    else h' = h
  action ingestTenantRoomEvent = {
    nondet evTenant = Set(1, 2).oneOf()
    ingestTenantRoomEventAs(evTenant)
  }
  action recordClaimedKey = if (h.gateStatus == 2 and h.claimedKeys < h.gateId)
    h' = h.with("claimedKeys", h.claimedKeys + 1)
    else h' = h
  action step = any { ingestTenantRoomEvent, recordClaimedKey }
  val safety = not(h.crossTenantRoomLeak) and h.claimedKeys <= h.gateId
  run marlinChatroomTenantAndGateTest = init.then(ingestTenantRoomEventAs(2)).then(ingestExternalAs(true, false, 1, 1)).then(agentRequestGate).then(resolveGateAs(false, true)).then(recordClaimedKey).then(agentInvokeTool).then(all { assert(not(h.crossTenantRoomLeak)), assert(h.claimedKeys == 1), assert(h.toolExecuted), assert(safety), h' = h })
}
