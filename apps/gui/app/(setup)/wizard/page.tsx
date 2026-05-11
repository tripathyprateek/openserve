"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { CheckCircle2, Circle, ArrowRight, Rocket, Key, Cloud, Zap } from "lucide-react"

const STEPS = [
  { id: 1, title: "Welcome", icon: Rocket },
  { id: 2, title: "Verify GCP", icon: Cloud },
  { id: 3, title: "First Model", icon: Zap },
  { id: 4, title: "API Key", icon: Key },
  { id: 5, title: "Done", icon: CheckCircle2 },
]

export default function WizardPage() {
  const router = useRouter()
  const [step, setStep] = useState(1)
  const [gcpProject, setGcpProject] = useState("")
  const modelId = "llama-3-8b-instruct"
  const [apiKeyName, setApiKeyName] = useState("")
  const [generatedKey, setGeneratedKey] = useState("")
  const [loading, setLoading] = useState(false)
  const [deploymentStarted, setDeploymentStarted] = useState(false)

  const next = () => setStep((s) => Math.min(s + 1, 5))

  async function deployModel() {
    setLoading(true)
    try {
      const res = await fetch(`${process.env.NEXT_PUBLIC_API_URL}/api/deployments`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          model_id: modelId,
          gpu_class: "l4",
          scale_to_zero: true,
          daily_usd_cap: "50",
          max_tokens_in: 8192,
          max_tokens_out: 4096,
        }),
      })
      if (res.ok) setDeploymentStarted(true)
    } finally {
      setLoading(false)
    }
  }

  async function createKey() {
    setLoading(true)
    try {
      const res = await fetch(`${process.env.NEXT_PUBLIC_API_URL}/api/keys`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ name: apiKeyName || "My first key", role: "developer" }),
      })
      if (res.ok) {
        const data = await res.json()
        setGeneratedKey(data.raw_key || "openserve_live_example_key_shown_once")
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="w-full max-w-2xl">
      {/* Step indicators */}
      <div className="flex items-center justify-center mb-8 gap-2">
        {STEPS.map((s, i) => (
          <div key={s.id} className="flex items-center gap-2">
            <div
              className={`flex items-center justify-center w-8 h-8 rounded-full text-sm font-medium transition-colors ${
                step > s.id
                  ? "bg-green-500 text-white"
                  : step === s.id
                  ? "bg-blue-500 text-white"
                  : "bg-gray-700 text-gray-400"
              }`}
            >
              {step > s.id ? <CheckCircle2 className="w-4 h-4" /> : s.id}
            </div>
            {i < STEPS.length - 1 && (
              <div className={`w-8 h-0.5 ${step > s.id ? "bg-green-500" : "bg-gray-700"}`} />
            )}
          </div>
        ))}
      </div>

      {/* Step 1: Welcome */}
      {step === 1 && (
        <Card className="bg-gray-900 border-gray-800 text-white">
          <CardHeader className="text-center">
            <div className="flex justify-center mb-4">
              <div className="p-4 bg-blue-500/20 rounded-full">
                <Rocket className="w-8 h-8 text-blue-400" />
              </div>
            </div>
            <CardTitle className="text-2xl">Welcome to openserve</CardTitle>
            <CardDescription className="text-gray-400 mt-2">
              Deploy open-source LLMs in your own GCP project. Your data never leaves your VPC.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {[
              "Open-source models from a curated catalog",
              "Scale to zero when idle — no surprise bills",
              "Per-key rate limits and daily spend caps",
              "Tamper-evident audit logs for SOC 2",
            ].map((item) => (
              <div key={item} className="flex items-center gap-3 text-sm text-gray-300">
                <CheckCircle2 className="w-4 h-4 text-green-400 flex-shrink-0" />
                {item}
              </div>
            ))}
          </CardContent>
          <CardFooter>
            <Button className="w-full" onClick={next}>
              Get started <ArrowRight className="ml-2 w-4 h-4" />
            </Button>
          </CardFooter>
        </Card>
      )}

      {/* Step 2: Verify GCP */}
      {step === 2 && (
        <Card className="bg-gray-900 border-gray-800 text-white">
          <CardHeader>
            <div className="flex justify-center mb-4">
              <div className="p-4 bg-blue-500/20 rounded-full">
                <Cloud className="w-8 h-8 text-blue-400" />
              </div>
            </div>
            <CardTitle className="text-2xl text-center">Verify GCP Setup</CardTitle>
            <CardDescription className="text-gray-400 text-center">
              Confirm your GCP project is configured correctly.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="project" className="text-gray-300">GCP Project ID</Label>
              <Input
                id="project"
                placeholder="my-gcp-project-123"
                value={gcpProject}
                onChange={(e) => setGcpProject(e.target.value)}
                className="bg-gray-800 border-gray-700 text-white"
              />
            </div>
            <div className="rounded-lg bg-gray-800 p-4 space-y-2 text-sm">
              <p className="font-medium text-gray-200">Prerequisites checklist:</p>
              {[
                "GKE cluster with GPU node pool created",
                "GCS bucket for model cache provisioned",
                "Cloud SQL (Postgres) with private IP",
                "Workload Identity enabled on cluster",
              ].map((item) => (
                <div key={item} className="flex items-center gap-2 text-gray-400">
                  <Circle className="w-3 h-3" /> {item}
                </div>
              ))}
              <p className="text-gray-500 text-xs mt-2">
                Run <code className="bg-gray-700 px-1 rounded">terraform apply</code> in{" "}
                <code className="bg-gray-700 px-1 rounded">examples/terraform/gcp-prereqs/</code> to set these up.
              </p>
            </div>
          </CardContent>
          <CardFooter className="flex gap-3">
            <Button variant="outline" className="flex-1 border-gray-700 text-gray-300" onClick={() => setStep(1)}>
              Back
            </Button>
            <Button className="flex-1" onClick={next}>
              Looks good <ArrowRight className="ml-2 w-4 h-4" />
            </Button>
          </CardFooter>
        </Card>
      )}

      {/* Step 3: Deploy First Model */}
      {step === 3 && (
        <Card className="bg-gray-900 border-gray-800 text-white">
          <CardHeader>
            <div className="flex justify-center mb-4">
              <div className="p-4 bg-blue-500/20 rounded-full">
                <Zap className="w-8 h-8 text-blue-400" />
              </div>
            </div>
            <CardTitle className="text-2xl text-center">Deploy Your First Model</CardTitle>
            <CardDescription className="text-gray-400 text-center">
              We&apos;ll deploy Llama-3-8B on an L4 GPU with scale-to-zero and a $50/day budget.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="rounded-lg bg-gray-800 p-4 space-y-3 text-sm">
              <div className="flex justify-between">
                <span className="text-gray-400">Model</span>
                <Badge variant="secondary">llama-3-8b-instruct</Badge>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-400">GPU</span>
                <Badge variant="secondary">NVIDIA L4 (24 GB)</Badge>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-400">Scale to zero</span>
                <Badge className="bg-green-500/20 text-green-400">Enabled (10 min idle)</Badge>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-400">Daily budget cap</span>
                <Badge variant="secondary">$50 / day</Badge>
              </div>
            </div>
            {deploymentStarted && (
              <div className="rounded-lg bg-green-500/10 border border-green-500/30 p-3 text-sm text-green-400">
                ✓ Deployment started! First replica ready in ~2 minutes while weights load from GCS.
              </div>
            )}
          </CardContent>
          <CardFooter className="flex gap-3">
            <Button variant="outline" className="flex-1 border-gray-700 text-gray-300" onClick={() => setStep(2)}>
              Back
            </Button>
            {!deploymentStarted ? (
              <Button className="flex-1" onClick={deployModel} disabled={loading}>
                {loading ? "Deploying…" : "Deploy model"}
              </Button>
            ) : (
              <Button className="flex-1" onClick={next}>
                Continue <ArrowRight className="ml-2 w-4 h-4" />
              </Button>
            )}
          </CardFooter>
        </Card>
      )}

      {/* Step 4: Create API Key */}
      {step === 4 && (
        <Card className="bg-gray-900 border-gray-800 text-white">
          <CardHeader>
            <div className="flex justify-center mb-4">
              <div className="p-4 bg-blue-500/20 rounded-full">
                <Key className="w-8 h-8 text-blue-400" />
              </div>
            </div>
            <CardTitle className="text-2xl text-center">Create an API Key</CardTitle>
            <CardDescription className="text-gray-400 text-center">
              Use this key to call your deployed model via the OpenAI-compatible API.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {!generatedKey ? (
              <div className="space-y-2">
                <Label htmlFor="keyname" className="text-gray-300">Key name</Label>
                <Input
                  id="keyname"
                  placeholder="My developer key"
                  value={apiKeyName}
                  onChange={(e) => setApiKeyName(e.target.value)}
                  className="bg-gray-800 border-gray-700 text-white"
                />
              </div>
            ) : (
              <div className="space-y-3">
                <div className="rounded-lg bg-yellow-500/10 border border-yellow-500/30 p-3 text-sm text-yellow-400">
                  ⚠ Copy this key now — it won&apos;t be shown again.
                </div>
                <div className="rounded-lg bg-gray-800 p-3 font-mono text-sm text-green-400 break-all">
                  {generatedKey}
                </div>
              </div>
            )}
          </CardContent>
          <CardFooter className="flex gap-3">
            <Button variant="outline" className="flex-1 border-gray-700 text-gray-300" onClick={() => setStep(3)}>
              Back
            </Button>
            {!generatedKey ? (
              <Button className="flex-1" onClick={createKey} disabled={loading}>
                {loading ? "Creating…" : "Create key"}
              </Button>
            ) : (
              <Button className="flex-1" onClick={next}>
                I&apos;ve saved it <ArrowRight className="ml-2 w-4 h-4" />
              </Button>
            )}
          </CardFooter>
        </Card>
      )}

      {/* Step 5: Done */}
      {step === 5 && (
        <Card className="bg-gray-900 border-gray-800 text-white">
          <CardHeader className="text-center">
            <div className="flex justify-center mb-4">
              <div className="p-4 bg-green-500/20 rounded-full">
                <CheckCircle2 className="w-8 h-8 text-green-400" />
              </div>
            </div>
            <CardTitle className="text-2xl">You&apos;re all set!</CardTitle>
            <CardDescription className="text-gray-400 mt-2">
              openserve is running in your GCP project. Try calling your model:
            </CardDescription>
          </CardHeader>
          <CardContent>
            <pre className="bg-gray-800 rounded-lg p-4 text-xs text-green-400 overflow-x-auto">
{`curl https://YOUR_DOMAIN/inference/llama-3-8b-instruct/v1/chat/completions \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"llama-3-8b-instruct","messages":[{"role":"user","content":"Hello!"}]}'`}
            </pre>
          </CardContent>
          <CardFooter>
            <Button className="w-full" onClick={() => router.push("/catalog")}>
              Go to dashboard <ArrowRight className="ml-2 w-4 h-4" />
            </Button>
          </CardFooter>
        </Card>
      )}
    </div>
  )
}
