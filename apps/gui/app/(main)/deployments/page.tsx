"use client";

import { useState } from "react";
import Link from "next/link";
import useSWR from "swr";
import { listDeployments, deleteDeployment, resumeDeployment } from "@/lib/api";
import { CheckCircle, ZapOff, AlertTriangle, Copy, Trash2, Play } from "lucide-react";

interface ConfirmDialog {
  deploymentId: string;
  deploymentName: string;
}

export default function DeploymentsPage() {
  const { data: deployments, isLoading, mutate } = useSWR(
    "/api/v1/deployments",
    () => listDeployments()
  );
  const [confirmDelete, setConfirmDelete] = useState<ConfirmDialog | null>(null);
  const [actionLoading, setActionLoading] = useState<Set<string>>(new Set());

  const getPhaseIcon = (
    phase: string
  ): React.ReactNode => {
    switch (phase) {
      case "Running":
        return <CheckCircle className="h-4 w-4 text-green-600" />;
      case "ScaledToZero":
        return <ZapOff className="h-4 w-4 text-yellow-600" />;
      case "BudgetPaused":
      case "Failed":
        return <AlertTriangle className="h-4 w-4 text-red-600" />;
      default:
        return <div className="h-4 w-4 rounded-full bg-gray-400" />;
    }
  };

  const getPhaseColor = (phase: string): string => {
    switch (phase) {
      case "Running":
        return "bg-green-50 text-green-800";
      case "ScaledToZero":
        return "bg-yellow-50 text-yellow-800";
      case "BudgetPaused":
      case "Failed":
        return "bg-red-50 text-red-800";
      default:
        return "bg-gray-50 text-gray-800";
    }
  };

  const copyEndpoint = (endpoint: string) => {
    navigator.clipboard.writeText(endpoint);
  };

  const handleDelete = async () => {
    if (!confirmDelete) return;

    setActionLoading((prev) => new Set(prev).add(confirmDelete.deploymentId));
    try {
      await deleteDeployment(confirmDelete.deploymentId);
      await mutate();
    } catch (error) {
      console.error("Failed to delete deployment:", error);
    } finally {
      setActionLoading((prev) => {
        const next = new Set(prev);
        next.delete(confirmDelete.deploymentId);
        return next;
      });
      setConfirmDelete(null);
    }
  };

  const handleResume = async (deploymentId: string) => {
    setActionLoading((prev) => new Set(prev).add(deploymentId));
    try {
      await resumeDeployment(deploymentId);
      await mutate();
    } catch (error) {
      console.error("Failed to resume deployment:", error);
    } finally {
      setActionLoading((prev) => {
        const next = new Set(prev);
        next.delete(deploymentId);
        return next;
      });
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-3xl font-bold text-foreground">Deployments</h1>
        <p className="mt-2 text-muted-foreground">
          Manage your running model deployments
        </p>
      </div>

      {/* Table */}
      <div className="overflow-x-auto rounded-lg border border-border bg-card">
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-muted/50">
            <tr>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Model
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                GPU
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Phase
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Replicas
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Today Spend
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Endpoint
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
            ) : deployments && deployments.length > 0 ? (
              deployments.map((deployment) => (
                <tr key={deployment.id} className="hover:bg-muted/50">
                  <td className="px-6 py-4 font-medium text-foreground">
                    <Link
                      href={"/deployments/" + deployment.id}
                      className="hover:text-primary hover:underline"
                    >
                      {deployment.modelName}
                    </Link>
                  </td>
                  <td className="px-6 py-4 text-muted-foreground">
                    {deployment.gpuClass}
                  </td>
                  <td className="px-6 py-4">
                    <div className="flex items-center gap-2">
                      {getPhaseIcon(deployment.phase)}
                      <span
                        className={`inline-flex rounded-full px-2 py-1 text-xs font-medium ${getPhaseColor(
                          deployment.phase
                        )}`}
                      >
                        {deployment.phase}
                      </span>
                    </div>
                  </td>
                  <td className="px-6 py-4 text-muted-foreground">
                    {deployment.replicas}
                  </td>
                  <td className="px-6 py-4 text-muted-foreground">
                    ${deployment.todaySpend.toFixed(2)}
                  </td>
                  <td className="px-6 py-4">
                    <div className="flex items-center gap-2">
                      <code className="rounded bg-muted px-2 py-1 font-mono text-xs text-muted-foreground">
                        {deployment.endpoint.substring(0, 20)}...
                      </code>
                      <button
                        onClick={() =>
                          copyEndpoint(deployment.endpoint)
                        }
                        className="rounded p-1 hover:bg-muted text-muted-foreground hover:text-foreground"
                      >
                        <Copy className="h-4 w-4" />
                      </button>
                    </div>
                  </td>
                  <td className="px-6 py-4">
                    <div className="flex items-center gap-2">
                      {deployment.phase === "BudgetPaused" && (
                        <button
                          onClick={() => handleResume(deployment.id)}
                          disabled={actionLoading.has(deployment.id)}
                          className="rounded p-1 hover:bg-muted text-muted-foreground hover:text-foreground disabled:opacity-50"
                          title="Resume"
                        >
                          <Play className="h-4 w-4" />
                        </button>
                      )}
                      <button
                        onClick={() =>
                          setConfirmDelete({
                            deploymentId: deployment.id,
                            deploymentName: deployment.modelName,
                          })
                        }
                        disabled={actionLoading.has(deployment.id)}
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
                  <p className="text-muted-foreground">
                    No deployments yet. Deploy a model from the catalog.
                  </p>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Confirmation Dialog */}
      {confirmDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-sm rounded-lg bg-card p-6 shadow-lg">
            <h2 className="text-lg font-semibold text-foreground">
              Delete deployment?
            </h2>
            <p className="mt-2 text-sm text-muted-foreground">
              Are you sure you want to delete the deployment for{" "}
              <strong>{confirmDelete.deploymentName}</strong>? This action cannot
              be undone.
            </p>
            <div className="mt-6 flex gap-2">
              <button
                onClick={() => setConfirmDelete(null)}
                disabled={actionLoading.has(confirmDelete.deploymentId)}
                className="flex-1 rounded-md border border-border px-4 py-2 text-sm font-medium text-foreground hover:bg-muted disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                onClick={handleDelete}
                disabled={actionLoading.has(confirmDelete.deploymentId)}
                className="flex-1 rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
              >
                {actionLoading.has(confirmDelete.deploymentId)
                  ? "Deleting..."
                  : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
