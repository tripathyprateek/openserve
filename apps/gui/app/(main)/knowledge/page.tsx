"use client"

import { useState, useRef } from "react"
import useSWR from "swr"
import { listDocuments, deleteDocument as apiDeleteDocument, uploadDocument, type KnowledgeDocument } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { BookOpen, Upload, Trash2, Loader2 } from "lucide-react"

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function StatusBadge({ status, errorMessage }: { status: string; errorMessage?: string }) {
  if (status === "ready") {
    return <Badge className="bg-green-500/20 text-green-400 border-0">Ready</Badge>
  }
  if (status === "processing") {
    return (
      <Badge className="bg-yellow-500/20 text-yellow-400 border-0 gap-1">
        <Loader2 className="w-3 h-3 animate-spin" />
        Processing
      </Badge>
    )
  }
  if (status === "error") {
    return (
      <Badge className="bg-red-500/20 text-red-400 border-0" title={errorMessage}>
        Error
      </Badge>
    )
  }
  return <Badge className="bg-gray-500/20 text-gray-400 border-0">Unknown</Badge>
}

export default function KnowledgePage() {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)
  const [uploadError, setUploadError] = useState("")

  const hasProcessing = (docs: KnowledgeDocument[]) => docs.some((d) => d.status === "processing")

  const { data, mutate } = useSWR<KnowledgeDocument[]>("documents", listDocuments, {
    refreshInterval: (docs) => {
      // Poll every 3 seconds if any document is processing
      return docs && hasProcessing(docs) ? 3000 : 0
    },
  })

  const documents = data ?? []

  async function handleFileSelected(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return

    setUploading(true)
    setUploadError("")

    try {
      await uploadDocument(file)
      mutate()
      if (fileInputRef.current) {
        fileInputRef.current.value = ""
      }
    } catch (err: unknown) {
      setUploadError(err instanceof Error ? err.message : "Failed to upload document")
    } finally {
      setUploading(false)
    }
  }

  async function deleteDoc(id: string, name: string) {
    if (!confirm(`Delete "${name}"? This cannot be undone.`)) return

    try {
      await apiDeleteDocument(id)
      mutate()
    } catch (err: unknown) {
      alert(err instanceof Error ? err.message : "Failed to delete document")
    }
  }

  return (
    <div className="p-6 max-w-5xl mx-auto space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-semibold flex items-center gap-2">
          <BookOpen className="w-6 h-6" /> Knowledge Base
        </h1>
        <p className="text-muted-foreground text-sm mt-1">
          Upload documents to ground model responses with your own data.
        </p>
      </div>

      {/* Upload Area */}
      <div className="rounded-lg border-2 border-dashed border-border bg-muted/30 p-8">
        <div className="flex flex-col items-center justify-center gap-3">
          <Upload className="w-8 h-8 text-muted-foreground" />
          <div className="text-center">
            <p className="font-medium text-foreground">Upload a document</p>
            <p className="text-sm text-muted-foreground mt-1">
              Supports .txt, .md, and .pdf files
            </p>
          </div>
          <Button
            onClick={() => fileInputRef.current?.click()}
            disabled={uploading}
            className="mt-2"
          >
            {uploading ? (
              <>
                <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                Uploading...
              </>
            ) : (
              <>
                <Upload className="w-4 h-4 mr-2" />
                Choose File
              </>
            )}
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            accept=".txt,.md,.pdf"
            onChange={handleFileSelected}
            className="hidden"
            disabled={uploading}
          />
          {uploadError && <p className="text-sm text-destructive mt-2">{uploadError}</p>}
        </div>
      </div>

      {/* Documents Table */}
      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Size</TableHead>
              <TableHead>Chunks</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {documents.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                  <div className="flex flex-col items-center gap-2">
                    <BookOpen className="w-8 h-8 opacity-50" />
                    <p>No documents yet. Upload a .txt, .md, or .pdf file to get started.</p>
                  </div>
                </TableCell>
              </TableRow>
            )}
            {documents.map((doc) => (
              <TableRow key={doc.id}>
                <TableCell className="font-medium">{doc.name}</TableCell>
                <TableCell className="text-sm text-muted-foreground">{doc.fileType}</TableCell>
                <TableCell className="text-sm text-muted-foreground">
                  {formatBytes(doc.fileSizeBytes)}
                </TableCell>
                <TableCell className="text-sm text-muted-foreground">{doc.chunkCount}</TableCell>
                <TableCell>
                  <StatusBadge status={doc.status} errorMessage={doc.errorMessage} />
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="text-destructive hover:text-destructive"
                    onClick={() => deleteDoc(doc.id, doc.name)}
                    disabled={doc.status === "processing"}
                  >
                    <Trash2 className="w-4 h-4" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
