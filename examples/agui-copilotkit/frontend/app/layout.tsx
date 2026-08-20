import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import { CopilotKit } from "@copilotkit/react-core";
import "@copilotkit/react-ui/styles.css";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Gantry + CopilotKit demo",
  description: "AG-UI frontend actions example (tool.DynamicClient)",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={`${geistSans.variable} ${geistMono.variable}`}>
      <body>
        {/*
          runtimeUrl points at this app's own same-origin Runtime route
          (app/api/copilotkit/route.ts). `agent` is the STRING id that route
          registered its HttpAgent under ("gantry_demo") -- not an HttpAgent
          instance. The browser never talks to the Gantry Go server directly.
        */}
        <CopilotKit runtimeUrl="/api/copilotkit" agent="gantry_demo">
          {children}
        </CopilotKit>
      </body>
    </html>
  );
}
