"use client";

import { useState } from "react";
import useSWR from "swr";
import {
  listAPIKeys,
  createAPIKey,
  deleteAPIKey,
  rotateAPIKey,
} from "@/lib/api";
import { Plus, Copy, Trash2, RotateCw } from "lucide-react";

interface NewKeyDialog {
  isOpen: boolean;
}

interface RevealKeyDialog {
  isOpen: boolean;
  key: string;
}

export default function KeysPage() {
  const { data: keys, isLoading, mutate } = useSWR("/api/v1/keys", () =>
    listAPIKeys()
  );

  const [newKeyDialog, setNewKeyDialog] = useState<NewKeyDialog>({ isOpen: false });
  const [revealKeyDialog, setRevealKeyDialog] = useState<RevealKeyDialog>({
    isOpen: false,
    key: "",
  });

  const [displayName, setDisplayName] = useState("");
  const [role, setRole] = useState("user");
  const [allowedDeployments, setAllowedDeployments] = useState<string[]>([]);
  const [rateLimitRpm, setRateLimitRpm] = useState("10000");
  const [rateLimitTpm, setRateLimitTpm] = useState("100000");
  const [expiryDate, setExpiryDate] = useState("");
  const [ipAllowlist, setIpAllowlist] = useState("");

  const [actionLoading, setActionLoading] = useState<Set<string>>(new Set());
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const handleCreateKey = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);

    if (!displayName.trim()) {
      setFormError("Display name is required");
      return;
    }

    setActionLoading((prev) => new Set(prev).add("create"));
    try {
      const ipList = ipAllowlist
        .split(",")
        .map((ip) => ip.trim())
        .filter((ip) => ip.length > 0);

      const result = await createAPIKey({
        displayName,
        role,
        allowedDeploymentIds: allowedDeployments,
        rateLimitRpm: parseInt(rateLimitRpm) || 10000,
        rateLimitTpm: parseInt(rateLimitTpm) || 100000,
        expiresAt: expiryDate ? new Date(expiryDate).toISOString() : undefined,
        ipAllowlist: ipList.length > 0 ? ipList : undefined,
      });

      setRevealKeyDialog({ isOpen: true, key: result.key });
      setNewKeyDialog({ isOpen: false });
      resetForm();
      await mutate();
    } catch (error) {
      setFormError(
        error instanceof Error ? error.message : "Failed to create API key"
      );
    } finally {
      setActionLoading((prev) => {
        const next = new Set(prev);
        next.delete("create");
        return next;
      });
    }
  };

  const handleDeleteKey = async (keyId: string) => {
    setActionLoading((prev) => new Set(prev).add(keyId));
    try {
      await deleteAPIKey(keyId);
      await mutate();
    } catch (error) {
      console.error("Failed to delete key:", error);
    } finally {
      setActionLoading((prev) => {
        const next = new Set(prev);
        next.delete(keyId);
        return next;
      });
    }
  };

  const handleRotateKey = async (keyId: string) => {
    setActionLoading((prev) => new Set(prev).add(keyId));
    try {
      const result = await rotateAPIKey(keyId);
      setRevealKeyDialog({ isOpen: true, key: result.key });
      await mutate();
    } catch (error) {
      console.error("Failed to rotate key:", error);
    } finally {
      setActionLoading((prev) => {
        const next = new Set(prev);
        next.delete(keyId);
        return next;
      });
    }
  };

  const resetForm = () => {
    setDisplayName("");
    setRole("user");
    setAllowedDeployments([]);
    setRateLimitRpm("10000");
    setRateLimitTpm("100000");
    setExpiryDate("");
    setIpAllowlist("");
    setFormError(null);
  };

  const copyToClipboard = (text: string, id: string) => {
    navigator.clipboard.writeText(text);
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold text-foreground">API Keys</h1>
          <p className="mt-2 text-muted-foreground">
            Manage API keys for accessing deployments
          </p>
        </div>
        <button
          onClick={() => setNewKeyDialog({ isOpen: true })}
          className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
        >
          <Plus className="h-4 w-4" />
          New Key
        </button>
      </div>

      {/* Keys Table */}
      <div className="overflow-x-auto rounded-lg border border-border bg-card">
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-muted/50">
            <tr>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Name
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Role
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Key
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Rate Limits
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Last Used
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Expires
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Actions
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {isLoading ? (
              <tr>
                <td colSpan={7} className="px-6 py-12 text-center">
                  <div className="inline-block h-8 w-8 animate-spin rounded-full border-4 border-muted border-t-primary" />
                </td>
              </tr>
            ) : keys && keys.length > 0 ? (
              keys.map((key) => (
                <tr key={key.id} className="hover:bg-muted/50">
                  <td className="px-6 py-4 font-medium text-foreground">
                    {key.displayName}
                  </td>
                  <td className="px-6 py-4">
                    <span className="inline-flex rounded-full bg-secondary/10 px-2 py-1 text-xs font-medium text-secondary-foreground">
                      {key.role}
                    </span>
                  </td>
                  <td className="px-6 py-4">
                    <div className="flex items-center gap-2">
                      <code className="rounded bg-muted px-2 py-1 font-mono text-xs text-muted-foreground">
                        {key.keyPrefix}...
                      </code>
                      <button
                        onClick={() => copyToClipboard(key.keyPrefix, key.id)}
                        className="rounded p-1 hover:bg-muted text-muted-foreground hover:text-foreground"
                      >
                        <Copy className="h-4 w-4" />
                      </button>
                    </div>
                  </td>
                  <td className="px-6 py-4 text-xs text-muted-foreground">
                    {key.rateLimitRpm}/min, {key.rateLimitTpm}/day
                  </td>
                  <td className="px-6 py-4 text-muted-foreground">
                    {key.lastUsedAt
                      ? new Date(key.lastUsedAt).toLocaleDateString()
                      : "Never"}
                  </td>
                  <td className="px-6 py-4 text-muted-foreground">
                    {key.expiresAt
                      ? new Date(key.expiresAt).toLocaleDateString()
                      : "Never"}
                  </td>
                  <td className="px-6 py-4">
                    <div className="flex items-center gap-2">
                      <button
                        onClick={() => handleRotateKey(key.id)}
                        disabled={actionLoading.has(key.id)}
                        className="rounded p-1 hover:bg-muted text-muted-foreground hover:text-foreground disabled:opacity-50"
                        title="Rotate"
                      >
                        <RotateCw className="h-4 w-4" />
                      </button>
                      <button
                        onClick={() => handleDeleteKey(key.id)}
                        disabled={actionLoading.has(key.id)}
                        className="rounded p-1 hover:bg-destructive/10 text-muted-foreground hover:text-destructive disabled:opacity-50"
                        title="Delete"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))
            ) : (
              <tr>
                <td colSpan={7} className="px-6 py-12 text-center">
                  <p className="text-muted-foreground">No API keys yet.</p>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* New Key Dialog */}
      {newKeyDialog.isOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-md rounded-lg bg-card p-6 shadow-lg max-h-96 overflow-y-auto">
            <h2 className="text-lg font-semibold text-foreground">
              Create API Key
            </h2>

            <form onSubmit={handleCreateKey} className="mt-4 space-y-4">
              {/* Display Name */}
              <div>
                <label className="block text-sm font-medium text-foreground">
                  Display Name
                </label>
                <input
                  type="text"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  disabled={actionLoading.has("create")}
                  className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
                  placeholder="My App Key"
                />
              </div>

              {/* Role */}
              <div>
                <label className="block text-sm font-medium text-foreground">
                  Role
                </label>
                <select
                  value={role}
                  onChange={(e) => setRole(e.target.value)}
                  disabled={actionLoading.has("create")}
                  className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
                >
                  <option value="user">User</option>
                  <option value="admin">Admin</option>
                </select>
              </div>

              {/* Rate Limits */}
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <label className="block text-sm font-medium text-foreground">
                    RPM
                  </label>
                  <input
                    type="number"
                    value={rateLimitRpm}
                    onChange={(e) => setRateLimitRpm(e.target.value)}
                    disabled={actionLoading.has("create")}
                    className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-foreground">
                    TPM
                  </label>
                  <input
                    type="number"
                    value={rateLimitTpm}
                    onChange={(e) => setRateLimitTpm(e.target.value)}
                    disabled={actionLoading.has("create")}
                    className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
                  />
                </div>
              </div>

              {/* Expires */}
              <div>
                <label className="block text-sm font-medium text-foreground">
                  Expires (Optional)
                </label>
                <input
                  type="date"
                  value={expiryDate}
                  onChange={(e) => setExpiryDate(e.target.value)}
                  disabled={actionLoading.has("create")}
                  className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
                />
              </div>

              {/* IP Allowlist */}
              <div>
                <label className="block text-sm font-medium text-foreground">
                  IP Allowlist (Optional)
                </label>
                <textarea
                  value={ipAllowlist}
                  onChange={(e) => setIpAllowlist(e.target.value)}
                  disabled={actionLoading.has("create")}
                  rows={2}
                  className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
                  placeholder="Comma-separated CIDRs"
                />
              </div>

              {/* Error */}
              {formError && (
                <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                  {formError}
                </div>
              )}

              {/* Actions */}
              <div className="flex gap-2 pt-4">
                <button
                  type="button"
                  onClick={() => {
                    setNewKeyDialog({ isOpen: false });
                    resetForm();
                  }}
                  disabled={actionLoading.has("create")}
                  className="flex-1 rounded-md border border-border px-4 py-2 text-sm font-medium text-foreground hover:bg-muted disabled:opacity-50"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={actionLoading.has("create")}
                  className="flex-1 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                >
                  {actionLoading.has("create") ? "Creating..." : "Create"}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Reveal Key Dialog */}
      {revealKeyDialog.isOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-md rounded-lg bg-card p-6 shadow-lg">
            <h2 className="text-lg font-semibold text-foreground">Your API Key</h2>
            <p className="mt-2 text-sm text-destructive font-medium">
              This key will not be shown again. Copy it now.
            </p>

            <div className="mt-4 rounded-md bg-muted p-4">
              <code className="break-all font-mono text-sm text-foreground">
                {revealKeyDialog.key}
              </code>
            </div>

            <div className="mt-6 flex gap-2">
              <button
                onClick={() =>
                  copyToClipboard(
                    revealKeyDialog.key,
                    "reveal-key"
                  )
                }
                className="flex-1 inline-flex items-center justify-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
              >
                <Copy className="h-4 w-4" />
                {copiedId === "reveal-key" ? "Copied!" : "Copy"}
              </button>
              <button
                onClick={() =>
                  setRevealKeyDialog({ isOpen: false, key: "" })
                }
                className="flex-1 rounded-md border border-border px-4 py-2 text-sm font-medium text-foreground hover:bg-muted"
              >
                Done
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
