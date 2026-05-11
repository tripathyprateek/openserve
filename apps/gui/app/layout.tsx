import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "openserve | LLM Platform",
  description:
    "An open-source LLM serving platform for deploying and managing large language models.",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className="min-h-screen bg-background font-sans antialiased">
        {children}
      </body>
    </html>
  );
}
