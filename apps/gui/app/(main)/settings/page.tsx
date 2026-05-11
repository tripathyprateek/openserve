"use client"

import { useState } from "react"
import useSWR from "swr"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import { Settings, Info, Trash2, Copy } from "lucide-react"
import {
  listWebhooks,
  createWebhook,
  deleteWebhook,
  listPromptTemplates,
  createPromptTemplate,
  deletePromptTemplate,
  type PromptTemplate,
} from "@/lib/api"

const VERSION = "0.1.0"

const EVENT_OPTIONS = [
  { value: "deployment.ready", label: "Deployment Ready" },
  { value: "deployment.budget_paused", label: "Budget Paused" },
  { value: "deployment.scale_zero", label: "Scale to Zero" },
  { value: "deployment.failed", label: "Deployment Failed" },
]

const EVENT_COLORS: Record<string, string> = {
  "deployment.ready": "bg-green-100 text-green-800",
  "deployment.budget_paused": "bg-orange-100 text-orange-800",
  "deployment.scale_zero": "bg-yellow-100 text-yellow-800",
  "deployment.failed": "bg-red-100 text-red-800",
}

function EventBadge({ event }: { event: string }) {
  return (
    <Badge variant="outline" className={EVENT_COLORS[event] || "bg-gray-100 text-gray-800"}>
      {event}
    </Badge>
  )
}

function PromptsSection() {
  const { data: templates = [], mutate } = useSWR("/api/v1/prompt-templates", () => listPromptTemplates())
  const [showForm, setShowForm] = useState(false)
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [content, setContent] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copiedId, setCopiedId] = useState<string | null>(null)

  const handleSubmit = async () => {
    if (!name || !content) {
      setError("Name and content are required")
      return
    }

    setLoading(true)
    setError(null)

    try {
      await createPromptTemplate({ name, description, content })
      setName("")
      setDescription("")
      setContent("")
      setShowForm(false)
      mutate()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create template")
    } finally {
      setLoading(false)
    }
  }

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this template?")) return
    try {
      await deletePromptTemplate(id)
      mutate()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete template")
    }
  }

  const copyContent = (template: PromptTemplate) => {
    navigator.clipboard.writeText(template.content)
    setCopiedId(template.id)
    setTimeout(() => setCopiedId(null), 2000)
  }

  return (
    <div className="space-y-4">
      <h2 className="text-lg font-medium">Prompt Templates</h2>

      <div className="rounded-lg bg-blue-50 border border-blue-200 p-4 space-y-2 text-sm">
        <div className="flex gap-2 items-start">
          <Info className="w-4 h-4 text-blue-600 flex-shrink-0 mt-0.5" />
          <p className="text-blue-900">
            Save reusable system prompts to use quickly in the deployment playground.
            Templates are organization-wide and can be loaded in the Model Parameters section.
          </p>
        </div>
      </div>

      {templates.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted">
              <tr>
                <th className="px-4 py-3 text-left font-medium">Name</th>
                <th className="px-4 py-3 text-left font-medium">Description</th>
                <th className="px-4 py-3 text-left font-medium">Preview</th>
                <th className="px-4 py-3 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {templates.map((template) => (
                <tr key={template.id} className="border-t hover:bg-muted/50">
                  <td className="px-4 py-3 font-medium">{template.name}</td>
                  <td className="px-4 py-3 text-muted-foreground text-xs">{template.description || "-"}</td>
                  <td className="px-4 py-3 text-xs font-mono text-muted-foreground max-w-xs truncate">
                    {template.content}
                  </td>
                  <td className="px-4 py-3 text-right space-x-2 flex justify-end">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => copyContent(template)}
                      className={`hover:text-blue-700 ${copiedId === template.id ? "text-green-600" : "text-blue-600"}`}
                      title="Copy content to clipboard"
                    >
                      <Copy className="w-4 h-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleDelete(template.id)}
                      className="text-red-600 hover:text-red-700"
                    >
                      <Trash2 className="w-4 h-4" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {!showForm ? (
        <Button onClick={() => setShowForm(true)}>Add Template</Button>
      ) : (
        <div className="border rounded-lg p-4 space-y-4 bg-muted/30">
          <div className="space-y-2">
            <Label htmlFor="template-name">Name</Label>
            <Input
              id="template-name"
              placeholder="e.g. Helpful Assistant"
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={loading}
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="template-description">Description (optional)</Label>
            <Input
              id="template-description"
              placeholder="Brief description of this template"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              disabled={loading}
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="template-content">Content</Label>
            <textarea
              id="template-content"
              placeholder="System prompt content..."
              value={content}
              onChange={(e) => setContent(e.target.value)}
              disabled={loading}
              className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground placeholder-muted-foreground focus:border-primary focus:outline-none resize-none"
              rows={4}
            />
          </div>

          {error && <p className="text-sm text-red-600">{error}</p>}

          <div className="flex gap-2">
            <Button onClick={handleSubmit} disabled={loading}>
              {loading ? "Creating..." : "Create"}
            </Button>
            <Button variant="outline" onClick={() => setShowForm(false)} disabled={loading}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

function WebhooksSection() {
  const { data: webhooks = [], mutate } = useSWR("/api/v1/webhooks", () => listWebhooks())
  const [showForm, setShowForm] = useState(false)
  const [url, setUrl] = useState("")
  const [selectedEvents, setSelectedEvents] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleEventToggle = (event: string) => {
    setSelectedEvents((prev) =>
      prev.includes(event) ? prev.filter((e) => e !== event) : [...prev, event]
    )
  }

  const handleSubmit = async () => {
    if (!url || selectedEvents.length === 0) {
      setError("URL and at least one event are required")
      return
    }

    if (!url.startsWith("https://")) {
      setError("URL must start with https://")
      return
    }

    setLoading(true)
    setError(null)

    try {
      await createWebhook({ url, events: selectedEvents })
      setUrl("")
      setSelectedEvents([])
      setShowForm(false)
      mutate()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create webhook")
    } finally {
      setLoading(false)
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await deleteWebhook(id)
      mutate()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete webhook")
    }
  }

  return (
    <div className="space-y-4">
      <h2 className="text-lg font-medium">Webhooks</h2>

      <div className="rounded-lg bg-blue-50 border border-blue-200 p-4 space-y-2 text-sm">
        <div className="flex gap-2 items-start">
          <Info className="w-4 h-4 text-blue-600 flex-shrink-0 mt-0.5" />
          <p className="text-blue-900">
            openserve sends a POST with JSON payload to your URL when events occur.
            Add an HMAC-SHA256 signature header (X-Openserve-Signature) for verification.
          </p>
        </div>
      </div>

      {webhooks.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted">
              <tr>
                <th className="px-4 py-3 text-left font-medium">URL</th>
                <th className="px-4 py-3 text-left font-medium">Events</th>
                <th className="px-4 py-3 text-left font-medium">Status</th>
                <th className="px-4 py-3 text-right font-medium">Action</th>
              </tr>
            </thead>
            <tbody>
              {webhooks.map((webhook) => (
                <tr key={webhook.id} className="border-t hover:bg-muted/50">
                  <td className="px-4 py-3 font-mono text-xs break-all">{webhook.url}</td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-2">
                      {webhook.events.map((event) => (
                        <EventBadge key={event} event={event} />
                      ))}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <Badge variant={webhook.enabled ? "default" : "secondary"}>
                      {webhook.enabled ? "Enabled" : "Disabled"}
                    </Badge>
                  </td>
                  <td className="px-4 py-3 text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleDelete(webhook.id)}
                      className="text-red-600 hover:text-red-700"
                    >
                      <Trash2 className="w-4 h-4" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {!showForm ? (
        <Button onClick={() => setShowForm(true)}>Add Webhook</Button>
      ) : (
        <div className="border rounded-lg p-4 space-y-4 bg-muted/30">
          <div className="space-y-2">
            <Label htmlFor="webhook-url">Webhook URL</Label>
            <Input
              id="webhook-url"
              type="url"
              placeholder="https://example.com/webhook"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              disabled={loading}
            />
            <p className="text-xs text-muted-foreground">Must start with https://</p>
          </div>

          <div className="space-y-2">
            <Label>Events to subscribe</Label>
            <div className="space-y-2">
              {EVENT_OPTIONS.map((option) => (
                <div key={option.value} className="flex items-center gap-2">
                  <Checkbox
                    id={option.value}
                    checked={selectedEvents.includes(option.value)}
                    onCheckedChange={() => handleEventToggle(option.value)}
                    disabled={loading}
                  />
                  <Label htmlFor={option.value} className="font-normal cursor-pointer">
                    {option.label}
                  </Label>
                </div>
              ))}
            </div>
          </div>

          {error && <p className="text-sm text-red-600">{error}</p>}

          <div className="flex gap-2">
            <Button onClick={handleSubmit} disabled={loading}>
              {loading ? "Creating..." : "Create"}
            </Button>
            <Button variant="outline" onClick={() => setShowForm(false)} disabled={loading}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

export default function SettingsPage() {
  return (
    <div className="p-6 max-w-2xl mx-auto space-y-8">
      <div>
        <h1 className="text-2xl font-semibold flex items-center gap-2">
          <Settings className="w-6 h-6" /> Settings
        </h1>
        <p className="text-muted-foreground text-sm mt-1">
          Manage your organization settings and integrations.
        </p>
      </div>

      {/* Organization Settings */}
      <div className="space-y-4">
        <h2 className="text-lg font-medium">Organization Settings</h2>
        <div className="border rounded-lg p-4 space-y-3">
          <div>
            <Label className="text-muted-foreground text-xs">Organization Name</Label>
            <p className="text-sm font-medium mt-1">Dev Org</p>
          </div>
        </div>
      </div>

      <Separator />

      {/* Prompt Templates */}
      <PromptsSection />

      <Separator />

      {/* Webhooks */}
      <WebhooksSection />

      <Separator />

      {/* Version info */}
      <div className="space-y-3">
        <h2 className="text-lg font-medium flex items-center gap-2">
          <Info className="w-4 h-4" /> About
        </h2>
        <div className="rounded-lg bg-muted p-4 space-y-2 text-sm">
          <div className="flex justify-between">
            <span className="text-muted-foreground">openserve version</span>
            <Badge variant="outline">v{VERSION}</Badge>
          </div>
          <div className="flex justify-between">
            <span className="text-muted-foreground">License</span>
            <span>Apache 2.0</span>
          </div>
          <div className="flex justify-between">
            <span className="text-muted-foreground">Source</span>
            <a
              href="https://github.com/openserve/openserve"
              className="text-blue-400 hover:underline"
              target="_blank"
              rel="noreferrer"
            >
              github.com/openserve/openserve
            </a>
          </div>
        </div>
      </div>
    </div>
  )
}
