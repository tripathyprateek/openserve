"use client";

import { useState, useEffect, useRef } from "react";
import { useParams, useRouter } from "next/navigation";
import useSWR from "swr";
import {
  getDeployment,
  searchWeb,
  type SearchResult,
  listConversations,
  createConversation,
  getConversation,
  appendMessage as apiAppendMessage,
  deleteConversation as apiDeleteConversation,
  type Conversation,
  listPromptTemplates,
  createPromptTemplate,
  type PromptTemplate,
  retrieveContext,
} from "@/lib/api";
import { CheckCircle, ZapOff, AlertTriangle, Copy, ArrowLeft, Send, Loader2, Trash2, BookOpen } from "lucide-react";

interface Message {
  role: "user" | "assistant" | "system";
  content: string;
  loading?: boolean;
}

export default function DeploymentDetailPage() {
  const params = useParams();
  const router = useRouter();
  const deploymentId = params.id as string;

  const { data: deployment, isLoading, error } = useSWR(
    deploymentId ? `/api/v1/deployments/${deploymentId}` : null,
    () => getDeployment(deploymentId)
  );

  const [messages, setMessages] = useState<Message[]>([]);
  const [inputValue, setInputValue] = useState("");
  const [isWaitingForResponse, setIsWaitingForResponse] = useState(false);
  const [playgroundApiKey, setPlaygroundApiKey] = useState("");
  const [webSearchEnabled, setWebSearchEnabled] = useState(false);
  const [ragEnabled, setRagEnabled] = useState(false);
  const [parametersOpen, setParametersOpen] = useState(false);
  const [temperature, setTemperature] = useState(0.7);
  const [maxTokens, setMaxTokens] = useState(2048);
  const [topP, setTopP] = useState(0.95);
  const [systemPrompt, setSystemPrompt] = useState("");
  const [promptTemplates, setPromptTemplates] = useState<PromptTemplate[]>([]);
  const [isLoadingTemplates, setIsLoadingTemplates] = useState(false);
  const [isSavingTemplate, setIsSavingTemplate] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  // Conversation history state
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null);
  const [isLoadingConversations, setIsLoadingConversations] = useState(false);

  // Load conversations and prompt templates when deployment is ready
  useEffect(() => {
    if (!deploymentId) return;
    const loadConversations = async () => {
      setIsLoadingConversations(true);
      try {
        const convs = await listConversations(deploymentId);
        setConversations(convs);
      } catch (err) {
        console.error("Failed to load conversations:", err);
      } finally {
        setIsLoadingConversations(false);
      }
    };
    const loadTemplates = async () => {
      setIsLoadingTemplates(true);
      try {
        const templates = await listPromptTemplates();
        setPromptTemplates(templates);
      } catch (err) {
        console.error("Failed to load prompt templates:", err);
      } finally {
        setIsLoadingTemplates(false);
      }
    };
    loadConversations();
    loadTemplates();
  }, [deploymentId]);

  const loadConversation = async (conversationId: string) => {
    try {
      const conv = await getConversation(conversationId);
      setActiveConversationId(conversationId);
      // Map conversation messages to our Message format
      const msgs: Message[] = (conv.messages || []).map((m) => ({
        role: m.role,
        content: m.content,
      }));
      setMessages(msgs);
    } catch (err) {
      console.error("Failed to load conversation:", err);
    }
  };

  const createNewConversation = async () => {
    try {
      const conv = await createConversation(deploymentId);
      setConversations((prev) => [conv, ...prev]);
      setActiveConversationId(conv.id);
      setMessages([]);
    } catch (err) {
      console.error("Failed to create conversation:", err);
    }
  };

  const deleteConversationHandler = async (conversationId: string) => {
    try {
      await apiDeleteConversation(conversationId);
      setConversations((prev) => prev.filter((c) => c.id !== conversationId));
      if (activeConversationId === conversationId) {
        setActiveConversationId(null);
        setMessages([]);
      }
    } catch (err) {
      console.error("Failed to delete conversation:", err);
    }
  };

  const loadPromptTemplate = (template: PromptTemplate) => {
    setSystemPrompt(template.content);
  };

  const saveAsPromptTemplate = async () => {
    if (!systemPrompt.trim()) {
      alert("System prompt is empty");
      return;
    }

    const templateName = prompt("Save as template named:");
    if (!templateName) return;

    setIsSavingTemplate(true);
    try {
      const newTemplate = await createPromptTemplate({
        name: templateName,
        description: "",
        content: systemPrompt,
      });
      setPromptTemplates((prev) => [newTemplate, ...prev]);
      alert("Template saved successfully");
    } catch (err) {
      console.error("Failed to save template:", err);
      alert("Failed to save template");
    } finally {
      setIsSavingTemplate(false);
    }
  };

  // Auto-scroll to bottom on new messages
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  // Initialize web search from deployment
  useEffect(() => {
    if (deployment) {
      setWebSearchEnabled(deployment.webSearchEnabled || false);
    }
  }, [deployment]);

  const handleSendMessage = async () => {
    if (!inputValue.trim() || isWaitingForResponse) return;

    const userMessage = { role: "user" as const, content: inputValue };
    const userContent = inputValue;
    setMessages((prev) => [...prev, userMessage]);
    setInputValue("");
    setIsWaitingForResponse(true);

    if (!playgroundApiKey.trim()) {
      setMessages((prev) => [
        ...prev,
        {
          role: "assistant",
          content: "Paste your API key above to test the deployment",
        },
      ]);
      setIsWaitingForResponse(false);
      return;
    }

    // Create or reuse conversation
    let conversationId = activeConversationId;
    if (!conversationId) {
      try {
        const conv = await createConversation(deploymentId, userContent.slice(0, 50));
        conversationId = conv.id;
        setActiveConversationId(conversationId);
        setConversations((prev) => [conv, ...prev]);
      } catch (err) {
        console.error("Failed to create conversation:", err);
        setIsWaitingForResponse(false);
        return;
      }
    }

    // Persist user message to conversation
    if (conversationId) {
      try {
        await apiAppendMessage(conversationId, "user", userContent);
      } catch (err) {
        console.error("Failed to append user message:", err);
      }
    }

    const gatewayUrl = process.env.NEXT_PUBLIC_GATEWAY_URL || "http://localhost:8081";

    // Add a placeholder assistant message we'll stream into
    setMessages((prev) => [...prev, { role: "assistant", content: "" }]);

    try {
      if (!deployment) {
        throw new Error("Deployment not loaded");
      }

      // Fetch RAG context if enabled
      let ragContext = "";
      if (ragEnabled && inputValue.trim()) {
        try {
          const chunks = await retrieveContext(inputValue.trim(), 5);
          if (chunks.length > 0) {
            ragContext = "Relevant knowledge base context:\n";
            chunks.forEach((chunk, i) => {
              ragContext += `[${i + 1}] ${chunk.content}\n\n`;
            });
            ragContext += "Answer based on the above context where relevant.\n\n";
          }
        } catch (ragErr) {
          console.warn("RAG retrieval failed:", ragErr);
        }
      }

      // Fetch web search results if enabled
      let searchContext = "";
      if (webSearchEnabled && inputValue.trim()) {
        try {
          const searchResults = await searchWeb(inputValue.trim(), 5);
          if (searchResults.results && searchResults.results.length > 0) {
            searchContext = "Web Search Results:\n";
            searchResults.results.forEach((result: SearchResult, index: number) => {
              searchContext += `${index + 1}. ${result.title}\n   URL: ${result.url}\n   ${result.description}\n\n`;
            });
          }
        } catch (searchErr) {
          // Log search error but continue with normal request
          console.warn("Web search failed:", searchErr);
        }
      }

      // Build messages with context injected and optional system prompt
      let requestMessages = [...messages, userMessage].map((m) => {
        // Inject RAG and search context into user message
        if (m.role === "user" && (ragContext || searchContext)) {
          return {
            role: m.role,
            content: `${ragContext}${searchContext ? searchContext + "\n" : ""}User question: ${m.content}`,
          };
        }
        return {
          role: m.role,
          content: m.content,
        };
      });

      // Prepend system prompt if provided
      if (systemPrompt.trim()) {
        requestMessages = [
          { role: "system" as "system", content: systemPrompt },
          ...requestMessages,
        ];
      }

      const requestBody: Record<string, unknown> = {
        model: deployment.modelId,
        messages: requestMessages,
        stream: true,
        temperature,
        top_p: topP,
        max_tokens: maxTokens,
      };

      const response = await fetch(`${gatewayUrl}/v1/chat/completions`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Authorization": `Bearer ${playgroundApiKey}`,
        },
        body: JSON.stringify(requestBody),
      });

      if (!response.ok) {
        const errText = await response.text();
        throw new Error(`Gateway error ${response.status}: ${errText}`);
      }

      const reader = response.body?.getReader();
      const decoder = new TextDecoder();
      let fullContent = "";

      if (reader) {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          const chunk = decoder.decode(value, { stream: true });
          const lines = chunk.split("\n");

          for (const line of lines) {
            if (!line.startsWith("data: ")) continue;
            const data = line.slice(6).trim();
            if (data === "[DONE]") break;

            try {
              const parsed = JSON.parse(data);
              const delta = parsed.choices?.[0]?.delta?.content ?? "";
              if (delta) {
                fullContent += delta;
                setMessages((prev) => {
                  const updated = [...prev];
                  updated[updated.length - 1] = {
                    role: "assistant",
                    content: fullContent,
                  };
                  return updated;
                });
              }
            } catch {
              // skip malformed SSE lines
            }
          }
        }
      }

      // Persist assistant response to conversation
      if (conversationId && fullContent) {
        try {
          await apiAppendMessage(conversationId, "assistant", fullContent);
        } catch (err) {
          console.error("Failed to append assistant message:", err);
        }
      }
    } catch (err) {
      const errMsg = err instanceof Error ? err.message : "Unknown error";
      const errorContent = `Error: ${errMsg}. Make sure the gateway is running and you have a valid API key.`;
      setMessages((prev) => {
        const updated = [...prev];
        updated[updated.length - 1] = {
          role: "assistant",
          content: errorContent,
        };
        return updated;
      });
    } finally {
      setIsWaitingForResponse(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSendMessage();
    }
  };

  const copyEndpoint = () => {
    if (deployment?.endpoint) {
      navigator.clipboard.writeText(deployment.endpoint);
    }
  };

  const getPhaseIcon = (phase: string): React.ReactNode => {
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

  if (error) {
    return (
      <div className="space-y-6">
        <button
          onClick={() => router.back()}
          className="flex items-center gap-2 text-primary hover:underline"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to Deployments
        </button>
        <div className="rounded-lg border border-destructive bg-destructive/10 p-6">
          <p className="text-destructive">Error loading deployment: {error.message}</p>
        </div>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="space-y-6">
        <button
          onClick={() => router.back()}
          className="flex items-center gap-2 text-primary hover:underline"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to Deployments
        </button>
        <div className="flex items-center justify-center h-96">
          <div className="inline-block h-8 w-8 animate-spin rounded-full border-4 border-muted border-t-primary" />
        </div>
      </div>
    );
  }

  if (!deployment) {
    return (
      <div className="space-y-6">
        <button
          onClick={() => router.back()}
          className="flex items-center gap-2 text-primary hover:underline"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to Deployments
        </button>
        <div className="text-muted-foreground">Deployment not found</div>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-screen space-y-6">
      {/* Back Button */}
      <button
        onClick={() => router.back()}
        className="inline-flex w-fit items-center gap-2 text-primary hover:underline"
      >
        <ArrowLeft className="h-4 w-4" />
        Back to Deployments
      </button>

      {/* Header Card */}
      <div className="rounded-lg border border-border bg-card p-6">
        <div className="grid grid-cols-2 gap-6 md:grid-cols-4">
          {/* Model Name */}
          <div>
            <p className="text-sm text-muted-foreground">Model</p>
            <p className="mt-1 text-lg font-semibold text-foreground">
              {deployment.modelName}
            </p>
          </div>

          {/* Phase */}
          <div>
            <p className="text-sm text-muted-foreground">Phase</p>
            <div className="mt-1 flex items-center gap-2">
              {getPhaseIcon(deployment.phase)}
              <span
                className={`inline-flex rounded-full px-2 py-1 text-xs font-medium ${getPhaseColor(
                  deployment.phase
                )}`}
              >
                {deployment.phase}
              </span>
            </div>
          </div>

          {/* GPU Class */}
          <div>
            <p className="text-sm text-muted-foreground">GPU Class</p>
            <p className="mt-1 text-lg font-semibold text-foreground">
              {deployment.gpuClass}
            </p>
          </div>

          {/* Endpoint */}
          <div>
            <p className="text-sm text-muted-foreground">Endpoint</p>
            <div className="mt-1 flex items-center gap-2">
              <button
                onClick={copyEndpoint}
                className="rounded p-1 hover:bg-muted text-muted-foreground hover:text-foreground"
                title="Copy endpoint"
              >
                <Copy className="h-4 w-4" />
              </button>
            </div>
          </div>
        </div>
      </div>

      {/* Chat Layout: Sidebar + Panel */}
      <div className="flex flex-1 gap-4 overflow-hidden">
        {/* Conversation History Sidebar */}
        <div className="w-60 flex flex-col border border-border rounded-lg bg-card overflow-hidden">
          {/* New Chat Button */}
          <button
            onClick={createNewConversation}
            className="m-4 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            New Chat
          </button>

          {/* Conversation List */}
          <div className="flex-1 overflow-y-auto px-2 pb-4 space-y-2">
            {isLoadingConversations ? (
              <div className="flex items-center justify-center h-12">
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
              </div>
            ) : conversations.length === 0 ? (
              <div className="px-2 py-4 text-xs text-muted-foreground text-center">
                No conversations yet
              </div>
            ) : (
              conversations.map((conv) => {
                const date = new Date(conv.createdAt);
                const dateStr =
                  date.toLocaleDateString("en-US", {
                    month: "short",
                    day: "numeric",
                  }) +
                  " " +
                  date.toLocaleTimeString("en-US", {
                    hour: "2-digit",
                    minute: "2-digit",
                  });

                return (
                  <div
                    key={conv.id}
                    className={`group flex items-center gap-2 rounded-lg px-2 py-2 text-sm cursor-pointer transition-colors ${
                      activeConversationId === conv.id
                        ? "bg-primary/10 text-foreground"
                        : "text-muted-foreground hover:bg-muted"
                    }`}
                  >
                    <button
                      onClick={() => loadConversation(conv.id)}
                      className="flex-1 text-left truncate"
                      title={conv.title}
                    >
                      <p className="truncate font-medium">{conv.title}</p>
                      <p className="truncate text-xs opacity-75">{dateStr}</p>
                    </button>
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        deleteConversationHandler(conv.id);
                      }}
                      className="rounded p-1 opacity-0 group-hover:opacity-100 hover:bg-destructive/10 hover:text-destructive transition-opacity"
                      title="Delete conversation"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>
                );
              })
            )}
          </div>
        </div>

        {/* Chat Playground Panel */}
        <div className="flex flex-1 flex-col overflow-hidden rounded-lg border border-border bg-card">
        {/* API Key Input */}
        <div className="border-b border-border bg-muted/30 p-4 space-y-3">
          <p className="text-sm text-muted-foreground">
            Playground uses your admin API key — requests count against rate limits and budget
          </p>
          <div className="flex gap-2">
            <input
              type="password"
              value={playgroundApiKey}
              onChange={(e) => setPlaygroundApiKey(e.target.value)}
              placeholder="Paste your API key here to test the deployment"
              className="flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground placeholder-muted-foreground focus:border-primary focus:outline-none"
            />
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="rag-toggle"
              checked={ragEnabled}
              onChange={(e) => setRagEnabled(e.target.checked)}
              className="h-4 w-4 rounded border-border bg-background text-primary focus:outline-none cursor-pointer"
            />
            <label htmlFor="rag-toggle" className="text-sm text-muted-foreground cursor-pointer flex items-center gap-1">
              <BookOpen className="w-4 h-4" />
              Enable Knowledge Base (retrieves relevant documents as context)
            </label>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="web-search-toggle"
              checked={webSearchEnabled}
              onChange={(e) => setWebSearchEnabled(e.target.checked)}
              className="h-4 w-4 rounded border-border bg-background text-primary focus:outline-none cursor-pointer"
            />
            <label htmlFor="web-search-toggle" className="text-sm text-muted-foreground cursor-pointer">
              Enable Web Search (provides real-time web results as context)
            </label>
          </div>

          {/* Model Parameters Collapsible */}
          <details
            open={parametersOpen}
            onToggle={(e) => setParametersOpen((e.target as HTMLDetailsElement).open)}
            className="border-t border-border pt-3"
          >
            <summary className="cursor-pointer text-sm font-medium text-muted-foreground hover:text-foreground">
              Model Parameters
            </summary>
            <div className="mt-3 space-y-3">
              {/* Temperature */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="text-xs text-muted-foreground">Temperature</label>
                  <span className="text-xs font-semibold text-foreground">{temperature.toFixed(1)}</span>
                </div>
                <input
                  type="range"
                  min="0"
                  max="2"
                  step="0.1"
                  value={temperature}
                  onChange={(e) => setTemperature(parseFloat(e.target.value))}
                  className="w-full h-2 bg-muted rounded-lg appearance-none cursor-pointer"
                />
                <p className="text-xs text-muted-foreground">Controls randomness (0.0 = deterministic, 2.0 = very random)</p>
              </div>

              {/* Top P */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="text-xs text-muted-foreground">Top P (Nucleus Sampling)</label>
                  <span className="text-xs font-semibold text-foreground">{topP.toFixed(2)}</span>
                </div>
                <input
                  type="range"
                  min="0"
                  max="1"
                  step="0.05"
                  value={topP}
                  onChange={(e) => setTopP(parseFloat(e.target.value))}
                  className="w-full h-2 bg-muted rounded-lg appearance-none cursor-pointer"
                />
                <p className="text-xs text-muted-foreground">Probability mass for token selection (lower = more focused)</p>
              </div>

              {/* Max Tokens */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="text-xs text-muted-foreground">Max Tokens</label>
                  <span className="text-xs font-semibold text-foreground">{maxTokens}</span>
                </div>
                <input
                  type="range"
                  min="128"
                  max="8192"
                  step="128"
                  value={maxTokens}
                  onChange={(e) => setMaxTokens(parseInt(e.target.value, 10))}
                  className="w-full h-2 bg-muted rounded-lg appearance-none cursor-pointer"
                />
                <p className="text-xs text-muted-foreground">Maximum tokens in response (128–8192)</p>
              </div>

              {/* System Prompt */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="text-xs text-muted-foreground">System Prompt</label>
                  <button
                    onClick={saveAsPromptTemplate}
                    disabled={isSavingTemplate || !systemPrompt.trim()}
                    className="text-xs px-2 py-1 rounded bg-primary/10 text-primary hover:bg-primary/20 disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    {isSavingTemplate ? "Saving..." : "Save as Preset"}
                  </button>
                </div>
                <textarea
                  value={systemPrompt}
                  onChange={(e) => setSystemPrompt(e.target.value)}
                  placeholder="Optional system message to guide model behavior..."
                  className="w-full rounded-lg border border-border bg-background px-3 py-2 text-xs text-foreground placeholder-muted-foreground focus:border-primary focus:outline-none resize-none"
                  rows={2}
                />
                <p className="text-xs text-muted-foreground">Prepended as a system message if provided</p>

                {/* Prompt Template Selector */}
                {promptTemplates.length > 0 && (
                  <div className="space-y-2 border-t border-border pt-3 mt-3">
                    <label className="text-xs text-muted-foreground">Load Preset</label>
                    {isLoadingTemplates ? (
                      <div className="flex items-center justify-center h-8">
                        <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />
                      </div>
                    ) : (
                      <div className="space-y-1">
                        {promptTemplates.map((template) => (
                          <button
                            key={template.id}
                            onClick={() => loadPromptTemplate(template)}
                            className="w-full text-left px-3 py-2 rounded text-xs hover:bg-muted bg-muted/50"
                            title={template.description || template.content}
                          >
                            <p className="font-medium truncate">{template.name}</p>
                            {template.description && (
                              <p className="text-muted-foreground text-xs truncate">{template.description}</p>
                            )}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
          </details>
        </div>

        {/* Message History */}
        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          {messages.length === 0 ? (
            <div className="flex h-full items-center justify-center">
              <p className="text-muted-foreground">
                Start a conversation with the model
              </p>
            </div>
          ) : (
            <>
              {messages.map((message, index) => (
                <div
                  key={index}
                  className={`flex ${
                    message.role === "user" ? "justify-end" : "justify-start"
                  }`}
                >
                  <div
                    className={`rounded-lg px-4 py-2 max-w-xs lg:max-w-md ${
                      message.role === "user"
                        ? "bg-primary/10 text-foreground"
                        : "bg-muted text-muted-foreground"
                    }`}
                  >
                    {message.role === "assistant" && message.content && (
                      <p className="text-sm italic">{message.content}</p>
                    )}
                    {message.role === "user" && (
                      <p className="text-sm">{message.content}</p>
                    )}
                  </div>
                </div>
              ))}
              <div ref={messagesEndRef} />
            </>
          )}
        </div>

        {/* Loading Indicator */}
        {isWaitingForResponse && (
          <div className="flex justify-start px-6 pb-2">
            <div className="flex items-center gap-2 rounded-lg bg-muted px-4 py-2">
              <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
              <span className="text-sm text-muted-foreground">Thinking...</span>
            </div>
          </div>
        )}

        {/* Input Area */}
        <div className="border-t border-border p-4">
          <div className="flex gap-2">
            <textarea
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Type your message (Shift+Enter for newline)..."
              className="flex-1 rounded-lg border border-border bg-background p-3 text-sm text-foreground placeholder-muted-foreground focus:border-primary focus:outline-none resize-none"
              rows={1}
              style={{ minHeight: "40px", maxHeight: "120px" }}
              disabled={isWaitingForResponse}
            />
            <button
              onClick={handleSendMessage}
              disabled={!inputValue.trim() || isWaitingForResponse}
              className="rounded-lg bg-primary p-3 text-primary-foreground hover:bg-primary/90 disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center"
            >
              <Send className="h-4 w-4" />
            </button>
          </div>
        </div>
        </div>
      </div>
    </div>
  );
}
