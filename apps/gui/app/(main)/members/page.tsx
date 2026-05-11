"use client"

import { useState } from "react"
import useSWR from "swr"
import { listMembers, inviteMember, removeMember as apiRemoveMember, type Member } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { UserPlus, Trash2, Users, ShieldCheck, Code2, Eye, Handshake } from "lucide-react"

const ROLES = [
  { value: "admin", label: "Admin", icon: ShieldCheck, color: "bg-purple-500/20 text-purple-400" },
  { value: "developer", label: "Developer", icon: Code2, color: "bg-blue-500/20 text-blue-400" },
  { value: "partner", label: "Partner", icon: Handshake, color: "bg-yellow-500/20 text-yellow-400" },
  { value: "viewer", label: "Viewer", icon: Eye, color: "bg-gray-500/20 text-gray-400" },
]

function RoleBadge({ role }: { role: string }) {
  const def = ROLES.find((r) => r.value === role) ?? ROLES[3]
  return (
    <Badge className={`${def.color} border-0 gap-1`}>
      <def.icon className="w-3 h-3" />
      {def.label}
    </Badge>
  )
}


export default function MembersPage() {
  const { data, mutate } = useSWR<Member[]>("members", listMembers)
  const members = data ?? []

  const [open, setOpen] = useState(false)
  const [email, setEmail] = useState("")
  const [role, setRole] = useState("developer")
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState("")

  async function invite() {
    setSubmitting(true)
    setError("")
    try {
      await inviteMember(email, role)
      setOpen(false)
      setEmail("")
      setRole("developer")
      mutate()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to send invite")
    } finally {
      setSubmitting(false)
    }
  }

  async function removeMember(id: string) {
    if (!confirm("Remove this member? They will lose access immediately.")) return
    await apiRemoveMember(id)
    mutate()
  }

  return (
    <div className="p-6 max-w-5xl mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold flex items-center gap-2">
            <Users className="w-6 h-6" /> Members
          </h1>
          <p className="text-muted-foreground text-sm mt-1">
            Manage who can access this openserve installation.
          </p>
        </div>

        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button>
              <UserPlus className="w-4 h-4 mr-2" /> Invite member
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Invite a member</DialogTitle>
              <DialogDescription>
                They&apos;ll receive an email with a sign-in link. Partners get scoped access only.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-2">
              <div className="space-y-2">
                <Label>Email address</Label>
                <Input
                  type="email"
                  placeholder="colleague@example.com"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </div>
              <div className="space-y-2">
                <Label>Role</Label>
                <Select value={role} onValueChange={setRole}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ROLES.map((r) => (
                      <SelectItem key={r.value} value={r.value}>
                        <span className="flex items-center gap-2">
                          <r.icon className="w-4 h-4" /> {r.label}
                        </span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  {role === "partner"
                    ? "Partners can only call deployments they have an API key for. No admin access."
                    : role === "admin"
                    ? "Admins can manage deployments, keys, members, and budgets."
                    : role === "developer"
                    ? "Developers can deploy models and create API keys."
                    : "Viewers can see deployments and usage but cannot make changes."}
                </p>
              </div>
              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
              <Button onClick={invite} disabled={submitting || !email}>
                {submitting ? "Sending…" : "Send invite"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>

      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name / Email</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Joined</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {members.length === 0 && (
              <TableRow>
                <TableCell colSpan={4} className="text-center text-muted-foreground py-8">
                  No members yet. Invite someone to get started.
                </TableCell>
              </TableRow>
            )}
            {members.map((m) => (
              <TableRow key={m.id}>
                <TableCell>
                  <div className="font-medium">{m.email}</div>
                </TableCell>
                <TableCell>
                  <RoleBadge role={m.role} />
                </TableCell>
                <TableCell className="text-muted-foreground text-sm">
                  {m.joinedAt ? new Date(m.joinedAt).toLocaleDateString() : "—"}
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="text-destructive hover:text-destructive"
                    onClick={() => removeMember(m.id)}
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
