// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/execution/agent-ifc-capability/AgentIfcCapability"

// Marlin downstream-specific Agent CaMeL/FIDES IFC overlay resolved
// transparently via quint-modules-resolver. Extends AgentIfcCapability with
// Tier-0 PromptGate human approval for tainted inputs and E-Stop interlock.
module marlin_agent_ifc_composed {
  type State = { humanGateApproved: bool, eStopTripped: bool, taintedUnapprovedExec: bool }
  action init = h' = { humanGateApproved: false, eStopTripped: false, taintedUnapprovedExec: false }
  action approveHumanToolGate = h' = h.with("humanGateApproved", true)
  action tripIfcEStop = h' = h.with("eStopTripped", true)
  action invokeGovernedTool = if (not(h.eStopTripped) and (not(h.dataTainted) or h.humanGateApproved) and (not(h.dataConfidential) or h.declassified))
    h' = h.with("toolInvoked", true).with("taintedUnapprovedExec", h.taintedUnapprovedExec or (h.dataTainted and not(h.humanGateApproved)) or h.eStopTripped)
    else h' = h
  action step = any { approveHumanToolGate, tripIfcEStop, invokeGovernedTool }
  val safety = not(h.taintedUnapprovedExec)
  run marlinAgentIfcGateTest = init.then(ingestUntrustedData).then(invokeGovernedTool).then(approveHumanToolGate).then(invokeGovernedTool).then(all { assert(h.toolInvoked), assert(not(h.taintedUnapprovedExec)), assert(safety), h' = h })
}
