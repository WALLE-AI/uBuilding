"use client";

import ReactMarkdown from "react-markdown";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { oneDark } from "react-syntax-highlighter/dist/esm/styles/prism";

interface MarkdownContentProps {
  content: string;
  /** "default" = bright assistant style; "thinking" = dimmed italic style */
  variant?: "default" | "thinking";
}

export default function MarkdownContent({ content, variant = "default" }: MarkdownContentProps) {
  const isThinking = variant === "thinking";

  const textColor = isThinking ? "text-gray-400/80" : "text-slate-800";
  const mutedColor = isThinking ? "text-gray-500/80" : "text-slate-500";
  const italicClass = isThinking ? "italic" : "";

  return (
    <div className={`prose-chat text-sm leading-relaxed ${italicClass}`}>
      <ReactMarkdown
        components={{
          /* ── Code ─────────────────────────────────────────────────────── */
          code({ className, children, ...props }) {
            const match = /language-(\w+)/.exec(className || "");
            const isBlock = !!match;
            if (isThinking || !isBlock) {
              return (
                <code
                  className={`${
                    isThinking
                      ? "bg-gray-800/60 text-gray-300/80"
                      : "bg-slate-100 text-indigo-700"
                  } px-1 py-0.5 rounded text-xs font-mono not-italic`}
                  {...props}
                >
                  {children}
                </code>
              );
            }
            return (
              <SyntaxHighlighter
                style={oneDark as Record<string, React.CSSProperties>}
                language={match[1]}
                PreTag="div"
                className="!my-2 !rounded-lg !text-xs"
              >
                {String(children).replace(/\n$/, "")}
              </SyntaxHighlighter>
            );
          },

          /* ── Paragraphs ───────────────────────────────────────────────── */
          p({ children }) {
            return <p className={`mb-2 last:mb-0 ${textColor}`}>{children}</p>;
          },

          /* ── Headings ─────────────────────────────────────────────────── */
          h1({ children }) {
            return (
              <h1 className={`text-xl font-bold ${textColor} mt-4 mb-2 pb-1 border-b border-indigo-700/40`}>
                {children}
              </h1>
            );
          },
          h2({ children }) {
            return (
              <h2 className={`text-lg font-bold ${textColor} mt-3 mb-2 pb-1 border-b border-gray-700/40`}>
                {children}
              </h2>
            );
          },
          h3({ children }) {
            return <h3 className={`text-base font-semibold ${textColor} mt-3 mb-1`}>{children}</h3>;
          },
          h4({ children }) {
            return <h4 className={`text-sm font-semibold ${mutedColor} mt-2 mb-1`}>{children}</h4>;
          },
          h5({ children }) {
            return <h5 className={`text-xs font-semibold ${mutedColor} mt-2 mb-1 uppercase tracking-wide`}>{children}</h5>;
          },
          h6({ children }) {
            return <h6 className={`text-xs font-medium ${mutedColor} mt-2 mb-1`}>{children}</h6>;
          },

          /* ── Inline formatting ────────────────────────────────────────── */
          strong({ children }) {
            return (
              <strong className={`font-semibold not-italic ${isThinking ? "text-gray-300/90" : "text-slate-900"}`}>
                {children}
              </strong>
            );
          },
          em({ children }) {
            return (
              <em className={`italic ${isThinking ? "text-gray-400/80" : "text-slate-700"}`}>
                {children}
              </em>
            );
          },

          /* ── Links ────────────────────────────────────────────────────── */
          a({ href, children }) {
            return (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className={`underline underline-offset-2 ${
                  isThinking
                    ? "text-indigo-400/70 hover:text-indigo-300/70"
                    : "text-indigo-600 hover:text-indigo-500"
                } transition-colors`}
              >
                {children}
              </a>
            );
          },

          /* ── Lists ────────────────────────────────────────────────────── */
          ul({ children }) {
            return (
              <ul className={`list-disc list-inside mb-2 space-y-0.5 ${textColor}`}>{children}</ul>
            );
          },
          ol({ children }) {
            return (
              <ol className={`list-decimal list-inside mb-2 space-y-0.5 ${textColor}`}>{children}</ol>
            );
          },
          li({ children }) {
            return <li className="leading-relaxed">{children}</li>;
          },

          /* ── Blockquote ───────────────────────────────────────────────── */
          blockquote({ children }) {
            return (
              <blockquote
                className={`border-l-2 ${
                  isThinking ? "border-purple-700/50" : "border-indigo-500"
                } pl-3 ${mutedColor} italic my-2`}
              >
                {children}
              </blockquote>
            );
          },

          /* ── Horizontal rule ──────────────────────────────────────────── */
          hr() {
            return <hr className="border-gray-700 my-3" />;
          },

          /* ── Tables ───────────────────────────────────────────────────── */
          table({ children }) {
            return (
              <div className="overflow-x-auto my-2">
                <table className="w-full text-xs border-collapse">{children}</table>
              </div>
            );
          },
          thead({ children }) {
            return <thead>{children}</thead>;
          },
          tbody({ children }) {
            return <tbody>{children}</tbody>;
          },
          tr({ children }) {
            return <tr className={`border-b ${isThinking ? "border-gray-700/50" : "border-slate-200"}`}>{children}</tr>;
          },
          th({ children }) {
            return (
              <th className={`px-3 py-1.5 text-left font-medium ${isThinking ? "text-gray-300 bg-gray-700/40" : "text-slate-700 bg-slate-100"}`}>
                {children}
              </th>
            );
          },
          td({ children }) {
            return (
              <td className={`px-3 py-1.5 ${mutedColor}`}>{children}</td>
            );
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
