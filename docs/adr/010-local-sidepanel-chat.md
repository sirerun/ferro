# ADR 010: Local standalone side-panel chat

Status: implemented on the local workflow branch; live-provider qualification pending.

The operator needs a usable browser interface without another agent application
or a second browser profile. The existing extension and Go executor already
provide explicit pairing, origin authorization and bounded task execution.

The Chrome side panel sends authenticated requests to `/chat/` on the existing
loopback bridge. The bridge requires its bearer token, exact listener Host and
an absent or extension Origin. It sends no CORS permission to websites. Pairing
may originate only from the extension popup or side-panel document.

The API is a narrow local consumer of Owner.Call, not another browser engine.
Each panel session has cancellation identity distinct from MCP clients. Tasks
share the existing serialized executor and tab leases. Read-only mode blocks
click/fill/select/key at the driver boundary even when a model requests them.
Navigation, reading and scrolling remain available, subject to the allowlist.
Interaction mode enables browser input for that task; it is not an outbound
approval system. Closing/interruption cancels the HTTP request; already
performed actions cannot be undone. Lost results are explicitly uncertain.
No automatic retry of a submitted task is allowed. A bounded process-local ID
ledger rejects duplicates; it is not a durable exactly-once guarantee.

Model settings are stored atomically with mode 0600 under FERRO_MCP_HOME.
Responses disclose only key presence. Keys are bound to the configured endpoint;
changing it clears the previous key. HTTPS is required except on a loopback IP
for local providers. Chrome retains the bridge token only in storage.session.
Chat text is bounded and retained in storage.local; clear/export are explicit.
Settings and transcripts are never stored in the repository. There is no cloud
identity or multi-user security claim. The local OS account is the administrator.

The panel retains a bounded local conversation archive (up to 30 chats and 100
messages per chat). It pairs one tab at a time; a user can switch by connecting
the current tab in Settings. The extension releases the previous pairing before
requesting the new one and attempts to restore the old pairing if the new pairing
is rejected.

The frame-colored background uses light/dark approximations plus an optional
custom color. Chrome owns the actual outer frame. Floating glass bubbles reuse
the supplied design without a JS UI framework or bundled HTMX. No idle model
calls, screenshots or page polling are added by the chat; the existing bridge
retains its single long-poll command connection. Model work is remote when a
remote provider is selected. Performance relative to another extension must be
measured rather than inferred from this architecture.

Verification includes the real unpacked extension UI in disposable Chrome with
a deterministic model endpoint, plus authentication/settings tests. The fixture
is transport and behavior evidence, not model-quality or real-site evidence.
