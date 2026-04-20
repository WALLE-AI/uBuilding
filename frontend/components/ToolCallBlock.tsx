"use client";

import { useState } from "react";
import { Wrench, Loader2, CheckCircle2, ChevronDown, ChevronRight, XCircle } from "lucide-react";

interface ToolCallBlockProps {
  name: string;
  status: "running" | "done";
  input?: string;
  result?: string;
}

function tryPrettyJson(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function isErrorResult(raw: string): boolean {
  try {
    const parsed = JSON.parse(raw);
    if (typeof parsed === "string") return parsed.toLowerCase().includes("error");
    return false;
  } catch {
    return false;
  }
}

export default function ToolCallBlock({ name, status, input, result }: ToolCallBlockProps) {
  const [expanded, setExpanded] = useState(false);

  const hasDetails = (input && input !== "null" && input !== "{}") || result;
  const resultIsError = result ? isErrorResult(result) : false;

  return (
    <div className="my-2 border border-orange-900/40 rounded-lg bg-gray-900/60 overflow-hidden">
      <button
        onClick={() => hasDetails && setExpanded((v) => !v)}
        className={`w-full flex items-center gap-2 px-3 py-2 text-left transition-colors ${
          hasDetails ? "hover:bg-gray-800/50 cursor-pointer" : "cursor-default"
        }`}
      >
        <Wrench size={13} className="flex-shrink-0 text-orange-400" />
        <span className="text-xs text-orange-300/90 font-medium font-mono flex-1 truncate">
          {name}
        </span>
        {status === "running" ? (
          <Loader2 size={13} className="flex-shrink-0 text-orange-400 animate-spin" />
        ) : resultIsError ? (
          <XCircle size={13} className="flex-shrink-0 text-red-400" />
        ) : (
          <CheckCircle2 size={13} className="flex-shrink-0 text-green-400" />
        )}
        {hasDetails && status === "done" && (
          expanded ? (
            <ChevronDown size={13} className="text-gray-500 flex-shrink-0" />
          ) : (
            <ChevronRight size={13} className="text-gray-500 flex-shrink-0" />
          )
        )}
      </button>

      {expanded && (
        <div className="border-t border-orange-900/20 divide-y divide-orange-900/10">
          {input && input !== "null" && input !== "{}" && (
            <div className="px-3 py-2">
              <p className="text-[10px] text-gray-500 uppercase tracking-wider mb-1">输入参数</p>
              <pre className="text-xs text-gray-300 font-mono whitespace-pre-wrap break-all leading-relaxed">
                {tryPrettyJson(input)}
              </pre>
            </div>
          )}
          {result && (
            <div className="px-3 py-2">
              <p className="text-[10px] text-gray-500 uppercase tracking-wider mb-1">执行结果</p>
              <pre
                className={`text-xs font-mono whitespace-pre-wrap break-all leading-relaxed ${
                  resultIsError ? "text-red-300" : "text-gray-300"
                }`}
              >
                {tryPrettyJson(result)}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
