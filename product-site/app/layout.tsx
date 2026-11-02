import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "the-M | Multi-Agent Orchestration Platform",
  description: "Build AI agent workflows without code. Visual builder, multi-tenant security, production-ready orchestration.",
  openGraph: {
    title: "the-M | Multi-Agent Orchestration Platform",
    description: "Build AI agent workflows without code.",
    type: "website",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className="dark">
      <body className="antialiased">{children}</body>
    </html>
  );
}
