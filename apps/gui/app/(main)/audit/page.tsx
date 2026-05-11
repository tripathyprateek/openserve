"use client";

import { useState } from "react";
import useSWR from "swr";
import { getAuditLog } from "@/lib/api";
import { ChevronDown } from "lucide-react";

export default function AuditPage() {
  const [before, setBefore] = useState<string | undefined>(undefined);
  const { data: auditLog, isLoading } = useSWR(
    ["/api/v1/audit", before],
    () => getAuditLog(50, before)
  );

  const handleLoadMore = () => {
    if (auditLog && auditLog.length > 0) {
      setBefore(auditLog[auditLog.length - 1].id);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-3xl font-bold text-foreground">Audit Log</h1>
        <p className="mt-2 text-muted-foreground">
          Track all actions and changes in your workspace
        </p>
      </div>

      {/* Audit Table */}
      <div className="overflow-x-auto rounded-lg border border-border bg-card">
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-muted/50">
            <tr>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Timestamp
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Actor
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Action
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Resource Type
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                Resource ID
              </th>
              <th className="px-6 py-3 text-left font-medium text-foreground">
                IP Address
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {isLoading ? (
              <tr>
                <td colSpan={6} className="px-6 py-12 text-center">
                  <div className="inline-block h-8 w-8 animate-spin rounded-full border-4 border-muted border-t-primary" />
                </td>
              </tr>
            ) : auditLog && auditLog.length > 0 ? (
              auditLog.map((entry) => (
                <tr key={entry.id} className="hover:bg-muted/50">
                  <td className="px-6 py-4 text-muted-foreground">
                    {new Date(entry.timestamp).toLocaleString()}
                  </td>
                  <td className="px-6 py-4">
                    <span className="font-mono text-xs">
                      {entry.actor.type === "user"
                        ? entry.actor.email
                        : `key:${entry.actor.keyPrefix}`}
                    </span>
                  </td>
                  <td className="px-6 py-4 font-medium text-foreground">
                    {entry.action}
                  </td>
                  <td className="px-6 py-4 text-muted-foreground">
                    {entry.resourceType}
                  </td>
                  <td className="px-6 py-4 font-mono text-xs text-muted-foreground">
                    {entry.resourceId}
                  </td>
                  <td className="px-6 py-4 text-muted-foreground">
                    {entry.ipAddress}
                  </td>
                </tr>
              ))
            ) : (
              <tr>
                <td colSpan={6} className="px-6 py-12 text-center">
                  <p className="text-muted-foreground">No audit entries found.</p>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Load More */}
      {auditLog && auditLog.length > 0 && (
        <div className="text-center">
          <button
            onClick={handleLoadMore}
            disabled={isLoading}
            className="inline-flex items-center gap-2 rounded-md border border-border px-4 py-2 text-sm font-medium text-foreground hover:bg-muted disabled:opacity-50"
          >
            <ChevronDown className="h-4 w-4" />
            Load More
          </button>
        </div>
      )}
    </div>
  );
}
