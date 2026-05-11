"use client";

import { useEffect, useState } from "react";
import { getUsage, type UsageResponse } from "@/lib/api";

export default function UsagePage() {
  const [timeRange, setTimeRange] = useState<"today" | "7d" | "30d">("today");
  const [usageData, setUsageData] = useState<UsageResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    getUsage()
      .then((data) => {
        setUsageData(data);
      })
      .catch((err) => {
        setError(err.message || "Failed to fetch usage data");
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  if (error) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold text-foreground">Usage & Spend</h1>
          <p className="mt-2 text-muted-foreground">
            Monitor usage statistics across your organization
          </p>
        </div>
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-900 dark:bg-red-950">
          <p className="text-sm font-medium text-red-800 dark:text-red-200">
            Error loading usage data: {error}
          </p>
        </div>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold text-foreground">Usage & Spend</h1>
          <p className="mt-2 text-muted-foreground">
            Monitor usage statistics across your organization
          </p>
        </div>
        <div className="rounded-lg border border-border bg-card p-12 text-center">
          <p className="text-muted-foreground">Loading usage data...</p>
        </div>
      </div>
    );
  }

  if (!usageData) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold text-foreground">Usage & Spend</h1>
          <p className="mt-2 text-muted-foreground">
            Monitor usage statistics across your organization
          </p>
        </div>
        <div className="rounded-lg border border-border bg-card p-12 text-center">
          <p className="text-muted-foreground">No data available</p>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header with Time Range Selector */}
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold text-foreground">Usage & Spend</h1>
          <p className="mt-2 text-muted-foreground">
            Monitor usage statistics across your organization
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => setTimeRange("today")}
            className={`px-3 py-1 rounded text-sm font-medium transition-colors ${
              timeRange === "today"
                ? "bg-primary text-primary-foreground"
                : "border border-border bg-card text-foreground hover:bg-muted"
            }`}
          >
            Today
          </button>
          <button
            onClick={() => setTimeRange("7d")}
            className={`px-3 py-1 rounded text-sm font-medium transition-colors ${
              timeRange === "7d"
                ? "bg-primary text-primary-foreground"
                : "border border-border bg-card text-foreground hover:bg-muted"
            }`}
          >
            7 days
          </button>
          <button
            onClick={() => setTimeRange("30d")}
            className={`px-3 py-1 rounded text-sm font-medium transition-colors ${
              timeRange === "30d"
                ? "bg-primary text-primary-foreground"
                : "border border-border bg-card text-foreground hover:bg-muted"
            }`}
          >
            30 days
          </button>
        </div>
      </div>

      {/* Summary Stats Row */}
      <div className="grid grid-cols-2 gap-4">
        {/* Total Requests */}
        <div className="rounded-lg border border-border bg-card p-6">
          <p className="text-sm font-medium text-muted-foreground">
            Total Requests (30 days)
          </p>
          <p className="mt-2 text-3xl font-bold text-foreground">
            {usageData.totalRequests.toLocaleString()}
          </p>
        </div>

        {/* Total This Month */}
        <div className="rounded-lg border border-border bg-card p-6">
          <p className="text-sm font-medium text-muted-foreground">
            This Month
          </p>
          <p className="mt-2 text-3xl font-bold text-foreground">
            {usageData.totalThisMonth.toLocaleString()}
          </p>
        </div>

        {/* Total Input Tokens */}
        <div className="rounded-lg border border-border bg-card p-6">
          <p className="text-sm font-medium text-muted-foreground">
            Total Input Tokens (30d)
          </p>
          <p className="mt-2 text-3xl font-bold text-foreground">
            {usageData.totalInputTokens.toLocaleString()}
          </p>
        </div>

        {/* Total Output Tokens */}
        <div className="rounded-lg border border-border bg-card p-6">
          <p className="text-sm font-medium text-muted-foreground">
            Total Output Tokens (30d)
          </p>
          <p className="mt-2 text-3xl font-bold text-foreground">
            {usageData.totalOutputTokens.toLocaleString()}
          </p>
        </div>
      </div>

      {/* Daily Requests Table */}
      <div className="rounded-lg border border-border bg-card">
        <div className="border-b border-border px-6 py-4">
          <h2 className="font-semibold text-foreground">Daily Inference Metrics (Last 30 Days)</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border bg-muted/50">
              <tr>
                <th className="px-6 py-3 text-left font-medium text-foreground">
                  Date
                </th>
                <th className="px-6 py-3 text-left font-medium text-foreground">
                  Requests
                </th>
                <th className="px-6 py-3 text-left font-medium text-foreground">
                  Input Tokens
                </th>
                <th className="px-6 py-3 text-left font-medium text-foreground">
                  Output Tokens
                </th>
              </tr>
            </thead>
            <tbody>
              {usageData.dailyRequests.length > 0 ? (
                usageData.dailyRequests.map((day, idx) => (
                  <tr
                    key={idx}
                    className="border-b border-border hover:bg-muted/50 transition-colors"
                  >
                    <td className="px-6 py-4 text-foreground font-medium">
                      {day.date}
                    </td>
                    <td className="px-6 py-4 text-foreground">
                      {day.requests.toLocaleString()}
                    </td>
                    <td className="px-6 py-4 text-foreground">
                      {day.inputTokens.toLocaleString()}
                    </td>
                    <td className="px-6 py-4 text-foreground">
                      {day.outputTokens.toLocaleString()}
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={4} className="px-6 py-8 text-center text-muted-foreground">
                    No data available for the selected period
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
