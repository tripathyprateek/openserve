import { Model } from "@/lib/api";
import { ChevronRight, ExternalLink, Download, Clock } from "lucide-react";

interface ModelCardProps {
  model: Model;
  onDeploy: (modelId: string) => void;
}

function formatDownloads(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}K`;
  return String(n);
}

function gpuLabel(cls?: string): string {
  if (!cls) return "GPU";
  const map: Record<string, string> = {
    l4: "L4",
    "a100-40g": "A100 40G",
    "a100-80g": "A100 80G",
  };
  return map[cls] ?? cls.toUpperCase();
}

function ctxLabel(n: number): string {
  if (n >= 1024) return `${Math.round(n / 1024)}K ctx`;
  return `${n} ctx`;
}

function coldStartLabel(parameterCount: string): string | null {
  // Parse the number from parameterCount (e.g., "8B" → 8, "70B" → 70, "27B" → 27)
  const match = parameterCount.match(/^([\d.]+)B$/i);
  if (!match) return null;

  const params = parseFloat(match[1]);
  if (params <= 9) return "~20s cold start";
  if (params < 30) return "~45s cold start";
  return "~90s cold start";
}

export function ModelCard({ model, onDeploy }: ModelCardProps) {
  return (
    <div className="flex flex-col rounded-lg border border-border bg-card p-6 shadow-sm transition-shadow hover:shadow-md">
      {/* Header */}
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="flex-1 min-w-0">
          <h3 className="text-lg font-semibold text-foreground leading-tight">
            {model.name}
          </h3>
          <p className="text-sm text-muted-foreground capitalize">{model.family}</p>
        </div>
        {model.hfRepo && (
          <a
            href={`https://huggingface.co/${model.hfRepo}`}
            target="_blank"
            rel="noopener noreferrer"
            className="shrink-0 text-muted-foreground hover:text-foreground transition-colors"
            title="View on HuggingFace"
          >
            <ExternalLink className="h-4 w-4" />
          </a>
        )}
      </div>

      {/* Badges */}
      <div className="mb-4 flex flex-wrap gap-1.5">
        <span className="inline-flex items-center rounded-full bg-primary/10 px-2.5 py-0.5 text-xs font-medium text-primary">
          {model.parameterCount}
        </span>
        <span className="inline-flex items-center rounded-full bg-secondary/10 px-2.5 py-0.5 text-xs font-medium text-secondary-foreground">
          {model.license}
        </span>
        <span className="inline-flex items-center rounded-full bg-muted px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
          {ctxLabel(model.contextLength)}
        </span>
        {model.minGPUClass && (
          <span className="inline-flex items-center rounded-full bg-amber-500/10 px-2.5 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400">
            {gpuLabel(model.minGPUClass)}
          </span>
        )}
        {(() => {
          const coldStart = coldStartLabel(model.parameterCount);
          if (!coldStart) return null;
          return (
            <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
              <Clock className="h-3 w-3" />
              {coldStart}
            </span>
          );
        })()}
      </div>

      {/* Description */}
      <p className="mb-4 line-clamp-2 text-sm text-muted-foreground">
        {model.description}
      </p>

      {/* Tags */}
      {model.tags.length > 0 && (
        <div className="mb-4 flex flex-wrap gap-1">
          {model.tags.slice(0, 3).map((tag) => (
            <span
              key={tag}
              className="inline-flex items-center rounded-md bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground"
            >
              {tag}
            </span>
          ))}
          {model.tags.length > 3 && (
            <span className="inline-flex items-center rounded-md bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
              +{model.tags.length - 3}
            </span>
          )}
        </div>
      )}

      {/* Footer: downloads + deploy */}
      <div className="mt-auto flex items-center justify-between gap-2">
        {model.downloads ? (
          <span className="flex items-center gap-1 text-xs text-muted-foreground">
            <Download className="h-3 w-3" />
            {formatDownloads(model.downloads)} downloads
          </span>
        ) : (
          <span />
        )}
        <button
          onClick={() => onDeploy(model.id)}
          className="inline-flex items-center justify-center gap-1.5 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
        >
          Deploy
          <ChevronRight className="h-4 w-4" />
        </button>
      </div>
    </div>
  );
}
