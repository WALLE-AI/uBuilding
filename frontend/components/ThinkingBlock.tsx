"use client";

import { useState } from "react";
import { Brain, ChevronDown, ChevronRight } from "lucide-react";
import MarkdownContent from "./MarkdownContent";

interface ThinkingBlockProps {
  content: string;
  streaming?: boolean;
}

export default function ThinkingBlock({ content, streaming }: ThinkingBlockProps) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="my-2 border border-purple-200 rounded-lg bg-purple-50/40 overflow-hidden">
      <button
        onClick={() => setExpanded((v) => !v)}
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-purple-50 transition-colors"
      >
        <Brain
          size={13}
          className={`flex-shrink-0 text-purple-500 ${streaming ? "animate-pulse" : ""}`}
        />
        <span className="text-xs text-purple-600 font-medium flex-1">
          {streaming ? "正在思考..." : "思考过程"}
        </span>
        {streaming ? (
          <span className="flex gap-0.5">
            {[0, 1, 2].map((i) => (
              <span
                key={i}
                className="w-1 h-1 bg-purple-400 rounded-full animate-bounce"
                style={{ animationDelay: `${i * 0.15}s` }}
              />
            ))}
          </span>
        ) : (
          expanded ? (
            <ChevronDown size={13} className="text-slate-400 flex-shrink-0" />
          ) : (
            <ChevronRight size={13} className="text-slate-400 flex-shrink-0" />
          )
        )}
      </button>

      {(expanded || streaming) && content && (
        <div className="px-3 pb-3 pt-1 border-t border-purple-100">
          <MarkdownContent content={content} variant="thinking" />
        </div>
      )}
    </div>
  );
}
