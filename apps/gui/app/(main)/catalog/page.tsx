"use client";

import { useState } from "react";
import useSWR from "swr";
import { getCatalog } from "@/lib/api";
import { ModelCard } from "@/components/model-card";
import { DeployDialog } from "@/components/deploy-dialog";
import { ShieldCheck, X, Search } from "lucide-react";

interface Toast {
  id: string;
  message: string;
  endpoint?: string;
}

export default function CatalogPage() {
  const { data: models, isLoading } = useSWR("/api/v1/catalog", () =>
    getCatalog()
  );
  const [selectedModel, setSelectedModel] = useState<string | null>(null);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [bannerDismissed, setBannerDismissed] = useState(false);
  const [search, setSearch] = useState("");
  const [gpuFilter, setGpuFilter] = useState<string>("all");

  const selectedModelObj = models?.find((m) => m.id === selectedModel);

  const filteredModels = models?.filter((m) => {
    const matchesSearch =
      search === "" ||
      m.name.toLowerCase().includes(search.toLowerCase()) ||
      m.family.toLowerCase().includes(search.toLowerCase()) ||
      m.tags.some((t) => t.toLowerCase().includes(search.toLowerCase()));
    const matchesGpu =
      gpuFilter === "all" ||
      (gpuFilter === "l4" && m.minGPUClass === "l4") ||
      (gpuFilter === "a100" && (m.minGPUClass === "a100-40g" || m.minGPUClass === "a100-80g"));
    return matchesSearch && matchesGpu;
  });

  const handleDeploySuccess = (endpoint: string) => {
    const id = Date.now().toString();
    setToasts((prev) => [
      ...prev,
      { id, message: "Deployment created successfully!", endpoint },
    ]);

    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
    }, 6000);

    setSelectedModel(null);
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-3xl font-bold text-foreground">Model Catalog</h1>
        <p className="mt-2 text-muted-foreground">
          Browse and deploy large language models — all inference runs inside your VPC
        </p>
      </div>

      {/* BYOC value-prop banner */}
      {!bannerDismissed && (
        <div className="relative flex items-start gap-3 rounded-lg border border-emerald-500/30 bg-emerald-500/5 px-4 py-3 text-sm">
          <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-emerald-500" />
          <div className="flex-1">
            <span className="font-medium text-emerald-700 dark:text-emerald-400">
              Your data never leaves your infrastructure.
            </span>{" "}
            <span className="text-muted-foreground">
              Every model runs inside your own GCP project — prompts, responses, and weights stay in your VPC.
              No data is processed on openserve servers.
            </span>
          </div>
          <button
            onClick={() => setBannerDismissed(true)}
            className="text-muted-foreground hover:text-foreground transition-colors"
            aria-label="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

      {/* Search + Filter bar */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search models, families, tags…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-md border border-border bg-card py-2 pl-9 pr-4 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40"
          />
        </div>
        <div className="flex gap-2">
          {(["all", "l4", "a100"] as const).map((val) => (
            <button
              key={val}
              onClick={() => setGpuFilter(val)}
              className={`rounded-md px-3 py-2 text-xs font-medium transition-colors ${
                gpuFilter === val
                  ? "bg-primary text-primary-foreground"
                  : "border border-border bg-card text-muted-foreground hover:text-foreground"
              }`}
            >
              {val === "all" ? "All GPUs" : val === "l4" ? "L4 (small)" : "A100 (large)"}
            </button>
          ))}
        </div>
      </div>

      {/* Models Grid */}
      {isLoading ? (
        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {[...Array(6)].map((_, i) => (
            <div
              key={i}
              className="h-64 rounded-lg border border-border bg-card p-6 animate-pulse"
            >
              <div className="h-4 w-32 rounded bg-muted" />
              <div className="mt-4 h-3 w-24 rounded bg-muted" />
              <div className="mt-4 h-3 w-full rounded bg-muted" />
              <div className="mt-2 h-3 w-5/6 rounded bg-muted" />
            </div>
          ))}
        </div>
      ) : filteredModels && filteredModels.length > 0 ? (
        <>
          <p className="text-xs text-muted-foreground">
            Showing {filteredModels.length} of {models?.length ?? 0} models
          </p>
          <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
            {filteredModels.map((model) => (
              <ModelCard
                key={model.id}
                model={model}
                onDeploy={setSelectedModel}
              />
            ))}
          </div>
        </>
      ) : (
        <div className="rounded-lg border border-border bg-card p-12 text-center">
          <p className="text-muted-foreground">
            {models && models.length > 0
              ? "No models match your search"
              : "No models available"}
          </p>
        </div>
      )}

      {/* Deploy Dialog */}
      {selectedModelObj && (
        <DeployDialog
          modelId={selectedModelObj.id}
          modelName={selectedModelObj.name}
          isOpen={!!selectedModel}
          onClose={() => setSelectedModel(null)}
          onSuccess={handleDeploySuccess}
        />
      )}

      {/* Toasts */}
      <div className="fixed bottom-4 right-4 space-y-2">
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className="rounded-lg border border-primary bg-primary/10 p-4 text-sm text-primary shadow-lg"
          >
            <p className="font-medium">{toast.message}</p>
            {toast.endpoint && (
              <p className="mt-1 break-all font-mono text-xs opacity-75">
                {toast.endpoint}
              </p>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
