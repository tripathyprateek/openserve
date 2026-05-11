"use client"

import { useState } from "react"
import useSWR from "swr"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import {
  Monitor,
  Trash2,
  RotateCw,
  Copy,
  ChevronDown,
  ChevronUp,
  Plus,
} from "lucide-react"
import {
  listPeers,
  createPeer,
  deletePeer,
  rotatePeerToken,
  listPeerInvites,
  createPeerInvite,
  deletePeerInvite,
  listAPIKeys,
  type Peer,
  type PeerInvite,
} from "@/lib/api"

function InstallCommand({
  token,
  domain,
}: {
  token: string
  domain: string
}) {
  const [copied, setCopied] = useState(false)
  const cmd = `curl -fsSL https://${domain}/peer-agent/install.sh | sh -s -- --token=${token} --relay=https://${domain}`

  const copy = () => {
    navigator.clipboard.writeText(cmd)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="space-y-1">
      <Label className="text-xs text-muted-foreground">
        Install command (token shown once)
      </Label>
      <div className="relative">
        <pre className="rounded-lg bg-zinc-900 text-zinc-100 text-xs p-3 pr-10 overflow-x-auto whitespace-pre-wrap break-all">
          {cmd}
        </pre>
        <button
          onClick={copy}
          className="absolute top-2 right-2 text-zinc-400 hover:text-zinc-200"
        >
          <Copy className="w-4 h-4" />
        </button>
      </div>
      {copied && <p className="text-xs text-green-600">Copied!</p>}
      <p className="text-xs text-amber-600 font-medium">
        ⚠ This token will not be shown again. Save it now.
      </p>
    </div>
  )
}

function PeerRow({
  peer,
  onDelete,
  domain,
}: {
  peer: Peer
  onDelete: () => void
  domain: string
}) {
  const [expanded, setExpanded] = useState(false)
  const [newToken, setNewToken] = useState<string | null>(null)
  const [rotating, setRotating] = useState(false)

  const { data: invites = [], mutate: mutateInvites } = useSWR(
    expanded ? `/api/v1/peers/${peer.id}/invites` : null,
    () => listPeerInvites(peer.id)
  )
  const { data: keys = [] } = useSWR("/api/v1/keys", () => listAPIKeys())

  const handleRotate = async () => {
    setRotating(true)
    try {
      const { token } = await rotatePeerToken(peer.id)
      setNewToken(token)
    } finally {
      setRotating(false)
    }
  }

  return (
    <div className="border rounded-lg overflow-hidden">
      <div className="flex items-center gap-3 px-4 py-3">
        <Monitor className="w-4 h-4 text-muted-foreground flex-shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="font-medium text-sm truncate">{peer.name}</p>
          <p className="text-xs text-muted-foreground">
            {peer.lastSeen
              ? `Last seen ${new Date(peer.lastSeen).toLocaleString()}`
              : "Never connected"}
          </p>
        </div>
        <Badge
          variant={peer.online ? "default" : "secondary"}
          className="flex-shrink-0"
        >
          {peer.online ? "🟢 Online" : "⚪ Offline"}
        </Badge>
        <div className="flex flex-wrap gap-1 max-w-xs">
          {(peer.models ?? []).slice(0, 3).map((m) => (
            <Badge key={m} variant="outline" className="text-xs font-mono">
              {m}
            </Badge>
          ))}
          {(peer.models ?? []).length > 3 && (
            <Badge variant="outline" className="text-xs">
              +{peer.models.length - 3}
            </Badge>
          )}
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? (
            <ChevronUp className="w-4 h-4" />
          ) : (
            <ChevronDown className="w-4 h-4" />
          )}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-red-600 hover:text-red-700"
          onClick={onDelete}
        >
          <Trash2 className="w-4 h-4" />
        </Button>
      </div>

      {expanded && (
        <div className="border-t bg-muted/30 px-4 py-4 space-y-4">
          {newToken ? (
            <InstallCommand token={newToken} domain={domain} />
          ) : (
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={handleRotate}
                disabled={rotating}
              >
                <RotateCw
                  className={`w-3 h-3 mr-1 ${
                    rotating ? "animate-spin" : ""
                  }`}
                />
                Rotate Token
              </Button>
              <span className="text-xs text-muted-foreground">
                Generates a new token; peer agent must restart.
              </span>
            </div>
          )}

          <Separator />

          <div className="space-y-2">
            <Label className="text-sm font-medium">Invited API Keys</Label>
            {invites.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                No API keys invited yet.
              </p>
            ) : (
              <div className="space-y-1">
                {invites.map((inv: PeerInvite) => (
                  <div
                    key={inv.id}
                    className="flex items-center justify-between text-sm"
                  >
                    <span className="font-mono text-xs">{inv.keyName}</span>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-red-600 hover:text-red-700 h-6 px-2"
                      onClick={async () => {
                        await deletePeerInvite(peer.id, inv.keyId)
                        mutateInvites()
                      }}
                    >
                      Remove
                    </Button>
                  </div>
                ))}
              </div>
            )}

            <select
              className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm mt-2"
              defaultValue=""
              onChange={async (e) => {
                if (!e.target.value) return
                await createPeerInvite(peer.id, e.target.value)
                mutateInvites()
                e.target.value = ""
              }}
            >
              <option value="">Add API key…</option>
              {keys
                .filter(
                  (k) =>
                    !invites.some(
                      (inv: PeerInvite) => inv.keyId === k.id
                    )
                )
                .map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.displayName}
                  </option>
                ))}
            </select>
          </div>
        </div>
      )}
    </div>
  )
}

export default function PeersPage() {
  const { data: peers = [], mutate } = useSWR("/api/v1/peers", () =>
    listPeers()
  )
  const [showForm, setShowForm] = useState(false)
  const [name, setName] = useState("")
  const [loading, setLoading] = useState(false)
  const [newPeer, setNewPeer] = useState<{
    token: string
    domain: string
  } | null>(null)

  const domain =
    typeof window !== "undefined"
      ? window.location.hostname
      : "openserve.example.com"

  const handleCreate = async () => {
    if (!name.trim()) return
    setLoading(true)
    try {
      const result = await createPeer(name.trim())
      setNewPeer({ token: result.token, domain })
      setName("")
      setShowForm(false)
      mutate()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="p-6 max-w-2xl mx-auto space-y-8">
      <div>
        <h1 className="text-2xl font-semibold flex items-center gap-2">
          <Monitor className="w-6 h-6" /> Local Peers
        </h1>
        <p className="text-muted-foreground text-sm mt-1">
          Run a model on your laptop and share it with your team via a secure
          tunnel.
        </p>
      </div>

      {newPeer && (
        <div className="border border-green-300 rounded-lg p-4 bg-green-50 space-y-3">
          <p className="text-sm font-medium text-green-900">
            Peer registered! Run this on your machine:
          </p>
          <InstallCommand token={newPeer.token} domain={newPeer.domain} />
          <Button variant="outline" size="sm" onClick={() => setNewPeer(null)}>
            Dismiss
          </Button>
        </div>
      )}

      <div className="space-y-3">
        {peers.map((peer) => (
          <PeerRow
            key={peer.id}
            peer={peer}
            domain={domain}
            onDelete={async () => {
              await deletePeer(peer.id)
              mutate()
            }}
          />
        ))}

        {peers.length === 0 && !showForm && (
          <div className="text-center py-12 text-muted-foreground border rounded-lg">
            <Monitor className="w-8 h-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">No local peers registered yet.</p>
            <p className="text-xs mt-1">
              Register your laptop to serve Ollama models to your team.
            </p>
          </div>
        )}
      </div>

      {showForm ? (
        <div className="border rounded-lg p-4 space-y-3 bg-muted/30">
          <Label htmlFor="peer-name">Peer Name</Label>
          <Input
            id="peer-name"
            placeholder="e.g. Alice's MacBook Pro"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && handleCreate()}
            disabled={loading}
          />
          <div className="flex gap-2">
            <Button onClick={handleCreate} disabled={loading || !name.trim()}>
              {loading ? "Registering…" : "Register"}
            </Button>
            <Button
              variant="outline"
              onClick={() => setShowForm(false)}
              disabled={loading}
            >
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <Button onClick={() => setShowForm(true)}>
          <Plus className="w-4 h-4 mr-2" /> Register Peer
        </Button>
      )}
    </div>
  )
}
