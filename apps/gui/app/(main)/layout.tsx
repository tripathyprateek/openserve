"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  BookOpen,
  Cpu,
  Key,
  Users,
  BarChart3,
  Shield,
  Monitor,
  Settings,
  LogOut,
} from "lucide-react";
import { logout } from "@/lib/api";

const navigation = [
  { name: "Catalog", href: "/catalog", icon: BookOpen },
  { name: "Deployments", href: "/deployments", icon: Cpu },
  { name: "Knowledge", href: "/knowledge", icon: BookOpen },
  { name: "API Keys", href: "/keys", icon: Key },
  { name: "Local Peers", href: "/peers", icon: Monitor },
  { name: "Members", href: "/members", icon: Users },
  { name: "Usage", href: "/usage", icon: BarChart3 },
  { name: "Audit Log", href: "/audit", icon: Shield },
  { name: "Settings", href: "/settings", icon: Settings },
];

export default function MainLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname();

  const isActive = (href: string): boolean => {
    return pathname === href || pathname.startsWith(href + "/");
  };

  return (
    <div className="flex h-screen bg-background">
      {/* Sidebar */}
      <aside className="w-64 border-r border-border bg-card">
        {/* Logo */}
        <div className="border-b border-border px-6 py-6">
          <h1 className="text-2xl font-bold text-primary">openserve</h1>
        </div>

        {/* Navigation */}
        <nav className="space-y-1 p-4">
          {navigation.map((item) => {
            const Icon = item.icon;
            const active = isActive(item.href);

            return (
              <Link
                key={item.href}
                href={item.href}
                className={`flex items-center gap-3 rounded-md px-4 py-2 text-sm font-medium transition-colors ${
                  active
                    ? "border-l-2 border-l-primary bg-primary/10 text-primary"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground"
                }`}
              >
                <Icon className="h-4 w-4 flex-shrink-0" />
                {item.name}
              </Link>
            );
          })}
        </nav>

        {/* Footer */}
        <div className="absolute bottom-0 w-64 border-t border-border px-4 py-4">
          <button
            onClick={() => logout()}
            className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <LogOut className="h-4 w-4" />
            Sign out
          </button>
        </div>
      </aside>

      {/* Main Content */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* Top Bar */}
        <header className="border-b border-border bg-card px-8 py-4">
          <div className="flex items-center justify-between">
            <h2 className="text-xl font-semibold text-foreground">Dashboard</h2>
            <div className="flex items-center gap-4">
              <button className="h-8 w-8 rounded-full bg-primary/20 text-primary hover:bg-primary/30">
                <span className="text-xs font-bold">U</span>
              </button>
            </div>
          </div>
        </header>

        {/* Content Area */}
        <main className="flex-1 overflow-auto bg-background">
          <div className="px-8 py-6">{children}</div>
        </main>
      </div>
    </div>
  );
}
