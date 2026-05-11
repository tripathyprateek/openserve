"use client";

import { useState } from "react";
import { CreateDeploymentRequest, createDeployment } from "@/lib/api";
import { X } from "lucide-react";

interface DeployDialogProps {
  modelId: string;
  modelName: string;
  isOpen: boolean;
  onClose: () => void;
  onSuccess: (endpoint: string) => void;
}

const GPU_OPTIONS = [
  { value: "l4", label: "L4", price: "$0.70/hr", note: "Best for 7B–13B models" },
  { value: "a100-40g", label: "A100 40G", price: "$2.50/hr", note: "For 30B–70B models" },
] as const;

export function DeployDialog({
  modelId,
  modelName,
  isOpen,
  onClose,
  onSuccess,
}: DeployDialogProps) {
  const [gpuClass, setGpuClass] = useState<string>(GPU_OPTIONS[0].value);
  const [scaleToZero, setScaleToZero] = useState<boolean>(false);
  const [idleTimeoutMinutes, setIdleTimeoutMinutes] = useState<number>(15);
  const [dailyBudgetCap, setDailyBudgetCap] = useState<string>("");
  const [maxInputTokens, setMaxInputTokens] = useState<string>("2000");
  const [maxOutputTokens, setMaxOutputTokens] = useState<string>("1000");
  const [webSearchEnabled, setWebSearchEnabled] = useState<boolean>(false);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  // Helper to get GPU option by value
  const getGpuOption = (value: string) => {
    return GPU_OPTIONS.find((opt) => opt.value === value) || GPU_OPTIONS[0];
  };

  // Calculate daily cost
  const calculateDailyCost = (gpuValue: string): string => {
    const gpuOpt = getGpuOption(gpuValue);
    // Extract numeric price from "$X.XX/hr"
    const pricePerHr = parseFloat(gpuOpt.price.match(/[\d.]+/)?.[0] || "0");
    const dailyCost = pricePerHr * 24;
    return `$${dailyCost.toFixed(2)}`;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    setError(null);

    try {
      const request: CreateDeploymentRequest = {
        modelId,
        gpuClass,
        scaleToZero,
        idleTimeoutMinutes,
        dailyBudgetCap: dailyBudgetCap ? parseFloat(dailyBudgetCap) : null,
        maxInputTokens: parseInt(maxInputTokens) || 2000,
        maxOutputTokens: parseInt(maxOutputTokens) || 1000,
        webSearchEnabled,
      };

      const result = await createDeployment(request);
      onSuccess(result.endpoint);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create deployment");
    } finally {
      setIsLoading(false);
    }
  };

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="w-full max-w-md rounded-lg bg-card p-6 shadow-lg">
        {/* Header */}
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold text-foreground">
            Deploy {modelName}
          </h2>
          <button
            onClick={onClose}
            disabled={isLoading}
            className="rounded-md p-1 text-muted-foreground hover:bg-muted disabled:opacity-50"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="space-y-4">
          {/* GPU Class */}
          <div>
            <label className="block text-sm font-medium text-foreground mb-3">
              GPU Class
            </label>
            <div className="grid grid-cols-2 gap-3">
              {GPU_OPTIONS.map((option) => (
                <button
                  key={option.value}
                  type="button"
                  onClick={() => setGpuClass(option.value)}
                  disabled={isLoading}
                  className={`rounded-lg border-2 p-3 text-left transition-colors ${
                    gpuClass === option.value
                      ? "border-primary bg-primary/5"
                      : "border-border bg-background hover:border-primary/50"
                  } disabled:opacity-50`}
                >
                  <div className="font-semibold text-foreground">{option.label}</div>
                  <div className="text-sm font-medium text-primary">{option.price}</div>
                  <div className="text-xs text-muted-foreground">{option.note}</div>
                </button>
              ))}
            </div>

            {/* Cost Estimate */}
            <div className="mt-3 rounded-md bg-muted/50 p-2.5 text-xs text-muted-foreground">
              {scaleToZero ? (
                <>Billed only when serving requests</>
              ) : (
                <>
                  ~{calculateDailyCost(gpuClass)}/day if always-on (24 hr)
                </>
              )}
            </div>
          </div>

          {/* Scale to Zero */}
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="scaleToZero"
              checked={scaleToZero}
              onChange={(e) => setScaleToZero(e.target.checked)}
              disabled={isLoading}
              className="rounded border border-border disabled:opacity-50"
            />
            <label htmlFor="scaleToZero" className="text-sm text-foreground">
              Scale to Zero when idle
            </label>
          </div>

          {/* Web Search */}
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="webSearch"
              checked={webSearchEnabled}
              onChange={(e) => setWebSearchEnabled(e.target.checked)}
              disabled={isLoading}
              className="rounded border border-border disabled:opacity-50"
            />
            <label htmlFor="webSearch" className="text-sm text-foreground">
              Enable Web Search (real-time context grounding)
            </label>
          </div>

          {/* Idle Timeout */}
          <div>
            <label className="block text-sm font-medium text-foreground">
              Idle Timeout (minutes): {idleTimeoutMinutes}
            </label>
            <input
              type="range"
              min="5"
              max="60"
              value={idleTimeoutMinutes}
              onChange={(e) => setIdleTimeoutMinutes(parseInt(e.target.value))}
              disabled={isLoading || !scaleToZero}
              className="mt-1 w-full disabled:opacity-50"
            />
          </div>

          {/* Daily Budget Cap */}
          <div>
            <label className="block text-sm font-medium text-foreground">
              Daily Budget Cap (USD)
            </label>
            <input
              type="number"
              step="0.01"
              value={dailyBudgetCap}
              onChange={(e) => setDailyBudgetCap(e.target.value)}
              disabled={isLoading}
              placeholder="Optional"
              className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
            />
          </div>

          {/* Max Input Tokens */}
          <div>
            <label className="block text-sm font-medium text-foreground">
              Max Input Tokens
            </label>
            <input
              type="number"
              value={maxInputTokens}
              onChange={(e) => setMaxInputTokens(e.target.value)}
              disabled={isLoading}
              className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
            />
          </div>

          {/* Max Output Tokens */}
          <div>
            <label className="block text-sm font-medium text-foreground">
              Max Output Tokens
            </label>
            <input
              type="number"
              value={maxOutputTokens}
              onChange={(e) => setMaxOutputTokens(e.target.value)}
              disabled={isLoading}
              className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
            />
          </div>

          {/* Error */}
          {error && (
            <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
              {error}
            </div>
          )}

          {/* Actions */}
          <div className="flex gap-2 pt-4">
            <button
              type="button"
              onClick={onClose}
              disabled={isLoading}
              className="flex-1 rounded-md border border-border px-4 py-2 text-sm font-medium text-foreground hover:bg-muted disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isLoading}
              className="flex-1 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              {isLoading ? "Deploying..." : "Deploy"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
