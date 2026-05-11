// Types
export interface Model {
  id: string;
  name: string;
  family: string;
  parameterCount: string;
  license: string;
  contextLength: number;
  description: string;
  tags: string[];
  minGPUClass?: string;
  hfRepo?: string;
  downloads?: number;
}

export interface Deployment {
  id: string;
  modelId: string;
  modelName: string;
  gpuClass: string;
  phase:
    | "Running"
    | "ScaledToZero"
    | "BudgetPaused"
    | "Failed"
    | "Initializing";
  replicas: number;
  endpoint: string;
  todaySpend: number;
  scaleToZero: boolean;
  idleTimeoutMinutes: number;
  dailyBudgetCap: number | null;
  maxInputTokens: number;
  maxOutputTokens: number;
  webSearchEnabled: boolean;
  createdAt: string;
}

export interface CreateDeploymentRequest {
  modelId: string;
  gpuClass: string;
  scaleToZero: boolean;
  idleTimeoutMinutes: number;
  dailyBudgetCap: number | null;
  maxInputTokens: number;
  maxOutputTokens: number;
  webSearchEnabled: boolean;
}

export interface SearchResult {
  title: string;
  url: string;
  description: string;
}

export interface SearchResponse {
  query: string;
  results: SearchResult[];
}

export interface APIKey {
  id: string;
  displayName: string;
  keyPrefix: string;
  role: string;
  allowedDeploymentIds: string[];
  rateLimitRpm: number;
  rateLimitTpm: number;
  lastUsedAt: string | null;
  expiresAt: string | null;
  createdAt: string;
}

export interface CreateAPIKeyRequest {
  displayName: string;
  role: string;
  allowedDeploymentIds: string[];
  rateLimitRpm: number;
  rateLimitTpm: number;
  expiresAt?: string;
  ipAllowlist?: string[];
}

export interface Member {
  id: string;
  email: string;
  role: string;
  joinedAt: string;
}

export interface AuditEntry {
  id: string;
  timestamp: string;
  actor: {
    type: "user" | "key";
    email?: string;
    keyPrefix?: string;
  };
  action: string;
  resourceType: string;
  resourceId: string;
  ipAddress: string;
  details?: Record<string, unknown>;
}

export interface UsageDayStat {
  date: string;
  requests: number;
  inputTokens: number;
  outputTokens: number;
}

export interface UsageResponse {
  dailyRequests: UsageDayStat[];
  totalRequests: number;
  totalThisMonth: number;
  totalInputTokens: number;
  totalOutputTokens: number;
}

export interface Webhook {
  id: string;
  url: string;
  events: string[];
  enabled: boolean;
  createdAt: string;
}

export interface CreateWebhookRequest {
  url: string;
  events: string[];
}

export interface OrgSettings {
  orgId: string;
  name: string;
  googleDomain: string;
  createdAt: string;
}

// API Client
const baseUrl = process.env.NEXT_PUBLIC_API_URL ?? "";

async function fetchAPI<T>(
  endpoint: string,
  options?: RequestInit
): Promise<T> {
  // When baseUrl is empty we use a relative path so the browser resolves
  // against the current origin (Next.js then rewrites via next.config.js).
  const url = baseUrl ? new URL(endpoint, baseUrl).toString() : endpoint;

  const response = await fetch(url, {
    ...options,
    credentials: "include",  // send session cookie
    headers: {
      "Content-Type": "application/json",
      ...options?.headers,
    },
  });

  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(
      `API error ${response.status}: ${errorText || response.statusText}`
    );
  }

  return response.json() as Promise<T>;
}

// Catalog
export async function getCatalog(): Promise<Model[]> {
  return fetchAPI<Model[]>("/api/v1/catalog");
}

// Deployments
export async function listDeployments(): Promise<Deployment[]> {
  return fetchAPI<Deployment[]>("/api/v1/deployments");
}

export async function createDeployment(
  body: CreateDeploymentRequest
): Promise<Deployment> {
  return fetchAPI<Deployment>("/api/v1/deployments", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export async function getDeployment(id: string): Promise<Deployment> {
  return fetchAPI<Deployment>(`/api/v1/deployments/${id}`);
}

export async function deleteDeployment(id: string): Promise<void> {
  await fetchAPI(`/api/v1/deployments/${id}`, {
    method: "DELETE",
  });
}

export async function resumeDeployment(id: string): Promise<void> {
  await fetchAPI(`/api/v1/deployments/${id}/resume`, {
    method: "POST",
  });
}

// API Keys
export async function listAPIKeys(): Promise<APIKey[]> {
  return fetchAPI<APIKey[]>("/api/v1/keys");
}

export async function createAPIKey(
  body: CreateAPIKeyRequest
): Promise<{ key: string } & APIKey> {
  return fetchAPI<{ key: string } & APIKey>("/api/v1/keys", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export async function deleteAPIKey(id: string): Promise<void> {
  await fetchAPI(`/api/v1/keys/${id}`, {
    method: "DELETE",
  });
}

export async function rotateAPIKey(id: string): Promise<{ key: string }> {
  return fetchAPI<{ key: string }>(`/api/v1/keys/${id}/rotate`, {
    method: "POST",
  });
}

// Members
export async function listMembers(): Promise<Member[]> {
  return fetchAPI<Member[]>("/api/v1/members");
}

export async function inviteMember(
  email: string,
  role: string
): Promise<Member> {
  return fetchAPI<Member>("/api/v1/members/invite", {
    method: "POST",
    body: JSON.stringify({ email, role }),
  });
}

export async function removeMember(id: string): Promise<void> {
  await fetchAPI(`/api/v1/members/${id}`, {
    method: "DELETE",
  });
}

// Audit Log
export async function getAuditLog(
  limit: number = 50,
  before?: string
): Promise<AuditEntry[]> {
  const params = new URLSearchParams();
  params.append("limit", limit.toString());
  if (before) {
    params.append("before", before);
  }

  return fetchAPI<AuditEntry[]>(`/api/v1/audit?${params.toString()}`);
}

// Usage
export async function getUsage(): Promise<UsageResponse> {
  return fetchAPI<UsageResponse>("/api/v1/usage");
}

// Settings
export async function getSettings(): Promise<OrgSettings> {
  return fetchAPI<OrgSettings>("/api/v1/settings");
}

export async function updateSettings(name: string): Promise<void> {
  return fetchAPI<void>("/api/v1/settings", {
    method: "POST",
    body: JSON.stringify({ name }),
  });
}

// Webhooks
export async function listWebhooks(): Promise<Webhook[]> {
  return fetchAPI<Webhook[]>("/api/v1/webhooks");
}

export async function createWebhook(body: CreateWebhookRequest): Promise<Webhook> {
  return fetchAPI<Webhook>("/api/v1/webhooks", { method: "POST", body: JSON.stringify(body) });
}

export async function deleteWebhook(id: string): Promise<void> {
  await fetchAPI(`/api/v1/webhooks/${id}`, { method: "DELETE" });
}

// Web Search
export async function searchWeb(query: string, count = 5): Promise<SearchResponse> {
  return fetchAPI<SearchResponse>("/api/v1/search", {
    method: "POST",
    body: JSON.stringify({ query, count }),
  });
}

// ── Peers ──────────────────────────────────────────────────────────────────

export interface Peer {
  id: string;
  name: string;
  ownerId: string;
  models: string[];
  online: boolean;
  lastSeen: string | null;
  createdAt: string;
}

export interface PeerInvite {
  id: string;
  keyId: string;
  keyName: string;
}

export interface CreatePeerResponse {
  id: string;
  token: string;
}

export async function listPeers(): Promise<Peer[]> {
  return fetchAPI<Peer[]>("/api/v1/peers");
}

export async function createPeer(name: string): Promise<CreatePeerResponse> {
  return fetchAPI<CreatePeerResponse>("/api/v1/peers", {
    method: "POST",
    body: JSON.stringify({ name }),
  });
}

export async function deletePeer(id: string): Promise<void> {
  return fetchAPI<void>(`/api/v1/peers/${id}`, { method: "DELETE" });
}

export async function rotatePeerToken(id: string): Promise<{ token: string }> {
  return fetchAPI<{ token: string }>(
    `/api/v1/peers/${id}/rotate-token`,
    { method: "POST" }
  );
}

export async function listPeerInvites(peerId: string): Promise<PeerInvite[]> {
  return fetchAPI<PeerInvite[]>(`/api/v1/peers/${peerId}/invites`);
}

export async function createPeerInvite(
  peerId: string,
  apiKeyId: string
): Promise<{ id: string }> {
  return fetchAPI<{ id: string }>(`/api/v1/peers/${peerId}/invites`, {
    method: "POST",
    body: JSON.stringify({ apiKeyId }),
  });
}

export async function deletePeerInvite(
  peerId: string,
  keyId: string
): Promise<void> {
  return fetchAPI<void>(`/api/v1/peers/${peerId}/invites/${keyId}`, {
    method: "DELETE",
  });
}

// Prompt Templates
export interface PromptTemplate {
  id: string;
  name: string;
  description: string;
  content: string;
  createdAt: string;
  createdBy?: string;
}

export interface CreatePromptTemplateRequest {
  name: string;
  description: string;
  content: string;
}

export interface UpdatePromptTemplateRequest {
  name?: string;
  description?: string;
  content?: string;
}

export async function listPromptTemplates(): Promise<PromptTemplate[]> {
  return fetchAPI<PromptTemplate[]>("/api/v1/prompt-templates");
}

export async function createPromptTemplate(
  body: CreatePromptTemplateRequest
): Promise<PromptTemplate> {
  return fetchAPI<PromptTemplate>("/api/v1/prompt-templates", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export async function updatePromptTemplate(
  id: string,
  body: UpdatePromptTemplateRequest
): Promise<void> {
  await fetchAPI<void>(`/api/v1/prompt-templates/${id}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}

export async function deletePromptTemplate(id: string): Promise<void> {
  await fetchAPI<void>(`/api/v1/prompt-templates/${id}`, {
    method: "DELETE",
  });
}

// Conversations
export interface ConversationMessage {
  id: string;
  conversationId: string;
  role: "user" | "assistant" | "system";
  content: string;
  createdAt: string;
}

export interface Conversation {
  id: string;
  deploymentId: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  messages?: ConversationMessage[];
}

export async function listConversations(
  deploymentId: string
): Promise<Conversation[]> {
  return fetchAPI<Conversation[]>(
    `/api/v1/conversations?deploymentId=${encodeURIComponent(deploymentId)}`
  );
}

export async function createConversation(
  deploymentId: string,
  title?: string
): Promise<Conversation> {
  return fetchAPI<Conversation>("/api/v1/conversations", {
    method: "POST",
    body: JSON.stringify({
      deploymentId,
      title: title ?? "New conversation",
    }),
  });
}

export async function getConversation(id: string): Promise<Conversation> {
  return fetchAPI<Conversation>(`/api/v1/conversations/${id}`);
}

export async function appendMessage(
  conversationId: string,
  role: string,
  content: string
): Promise<ConversationMessage> {
  return fetchAPI<ConversationMessage>(
    `/api/v1/conversations/${conversationId}/messages`,
    {
      method: "POST",
      body: JSON.stringify({ role, content }),
    }
  );
}

export async function deleteConversation(id: string): Promise<void> {
  return fetchAPI<void>(`/api/v1/conversations/${id}`, {
    method: "DELETE",
  });
}

// Knowledge Base (RAG)
export interface KnowledgeDocument {
  id: string
  name: string
  fileType: string
  fileSizeBytes: number
  chunkCount: number
  status: "processing" | "ready" | "error"
  errorMessage?: string
  createdAt: string
}

export interface RetrievedChunk {
  documentId: string
  chunkIndex: number
  content: string
  score: number
}

export async function listDocuments(): Promise<KnowledgeDocument[]> {
  return fetchAPI<KnowledgeDocument[]>("/api/v1/documents")
}

export async function deleteDocument(id: string): Promise<void> {
  return fetchAPI<void>(`/api/v1/documents/${id}`, {
    method: "DELETE",
  })
}

export async function retrieveContext(query: string, topK = 5): Promise<RetrievedChunk[]> {
  const res = await fetchAPI<{ chunks: RetrievedChunk[] }>("/api/v1/rag/retrieve", {
    method: "POST",
    body: JSON.stringify({ query, topK }),
  })
  return res?.chunks ?? []
}

export async function uploadDocument(file: File): Promise<{ id: string; status: string }> {
  const form = new FormData()
  form.append("file", file)
  const baseUrl = process.env.NEXT_PUBLIC_API_URL ?? ""
  const url = baseUrl ? new URL("/api/v1/documents", baseUrl).toString() : "/api/v1/documents"
  const resp = await fetch(url, {
    method: "POST",
    body: form,
    credentials: "include",  // send session cookie
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

// Logout clears the session cookie and redirects to home
export async function logout(): Promise<void> {
  await fetch(`${baseUrl}/api/v1/auth/logout`, {
    method: "POST",
    credentials: "include",
  })
  // Reload to clear any client-side state
  if (typeof window !== "undefined") {
    window.location.href = "/"
  }
}
