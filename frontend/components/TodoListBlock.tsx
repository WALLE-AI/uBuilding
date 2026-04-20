"use client";

import { CheckCircle2, Circle, Loader2, XCircle, ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";
import type { TodoItem } from "@/types/chat";

interface TodoListBlockProps {
  todos: TodoItem[];
  status: "running" | "done";
}

const STATUS_CONFIG = {
  completed: {
    icon: CheckCircle2,
    iconClass: "text-emerald-500",
    textClass: "text-slate-400 line-through",
    bg: "bg-emerald-50",
    border: "border-emerald-200",
    label: "完成",
  },
  in_progress: {
    icon: Loader2,
    iconClass: "text-indigo-500 animate-spin",
    textClass: "text-slate-800 font-medium",
    bg: "bg-indigo-50",
    border: "border-indigo-200",
    label: "进行中",
  },
  pending: {
    icon: Circle,
    iconClass: "text-slate-300",
    textClass: "text-slate-500",
    bg: "bg-slate-50",
    border: "border-slate-200",
    label: "待办",
  },
  failed: {
    icon: XCircle,
    iconClass: "text-red-400",
    textClass: "text-slate-400 line-through",
    bg: "bg-red-50",
    border: "border-red-200",
    label: "失败",
  },
} as const;

const PRIORITY_CONFIG = {
  high:   { dot: "bg-red-400",    label: "高" },
  medium: { dot: "bg-amber-400",  label: "中" },
  low:    { dot: "bg-slate-300",  label: "低" },
} as const;

function progressBar(todos: TodoItem[]) {
  if (!todos.length) return { done: 0, total: 0, pct: 0 };
  const done = todos.filter((t) => t.status === "completed").length;
  return { done, total: todos.length, pct: Math.round((done / todos.length) * 100) };
}

export default function TodoListBlock({ todos, status }: TodoListBlockProps) {
  const [expanded, setExpanded] = useState(true);
  const { done, total, pct } = progressBar(todos);

  const inProgress = todos.filter((t) => t.status === "in_progress");
  const pending    = todos.filter((t) => t.status === "pending");
  const completed  = todos.filter((t) => t.status === "completed");
  const failed     = todos.filter((t) => t.status === "failed");
  const ordered    = [...inProgress, ...pending, ...completed, ...failed];

  return (
    <div className="my-2 border border-indigo-200 rounded-xl bg-indigo-50/30 overflow-hidden">
      {/* Header */}
      <button
        onClick={() => setExpanded((v) => !v)}
        className="w-full flex items-center gap-2.5 px-3 py-2.5 text-left hover:bg-indigo-50 transition-colors"
      >
        {/* Icon */}
        <span className="w-5 h-5 rounded-md bg-indigo-100 flex items-center justify-center shrink-0">
          <CheckCircle2 size={12} className="text-indigo-600" />
        </span>

        <span className="text-xs font-semibold text-indigo-700 flex-1">
          待办事项
        </span>

        {/* Progress badge */}
        <span className="text-[11px] text-indigo-500 font-medium">
          {done}/{total} 完成
        </span>

        {/* Streaming pulse */}
        {status === "running" && (
          <span className="flex gap-0.5 ml-1">
            {[0, 1, 2].map((i) => (
              <span
                key={i}
                className="w-1 h-1 bg-indigo-400 rounded-full animate-bounce"
                style={{ animationDelay: `${i * 0.15}s` }}
              />
            ))}
          </span>
        )}

        {expanded ? (
          <ChevronDown size={13} className="text-slate-400 shrink-0" />
        ) : (
          <ChevronRight size={13} className="text-slate-400 shrink-0" />
        )}
      </button>

      {/* Progress bar */}
      {expanded && total > 0 && (
        <div className="px-3 pb-1">
          <div className="h-1 rounded-full bg-indigo-100 overflow-hidden">
            <div
              className="h-full rounded-full bg-indigo-400 transition-all duration-500"
              style={{ width: `${pct}%` }}
            />
          </div>
        </div>
      )}

      {/* Todo list */}
      {expanded && ordered.length > 0 && (
        <div className="px-2 pb-2 space-y-1 border-t border-indigo-100 pt-2">
          {ordered.map((item) => {
            const sc = STATUS_CONFIG[item.status] ?? STATUS_CONFIG.pending;
            const pc = item.priority ? (PRIORITY_CONFIG[item.priority] ?? PRIORITY_CONFIG.medium) : null;
            const Icon = sc.icon;
            const displayText = item.status === "in_progress" && item.activeForm
              ? item.activeForm
              : item.content;

            return (
              <div
                key={item.id}
                className={`flex items-start gap-2 px-2 py-1.5 rounded-lg border ${sc.bg} ${sc.border}`}
              >
                <Icon size={14} className={`${sc.iconClass} shrink-0 mt-0.5`} />
                <span className={`flex-1 text-xs leading-relaxed ${sc.textClass}`}>
                  {displayText}
                </span>
                {pc && (
                  <span className="flex items-center gap-1 shrink-0">
                    <span className={`w-1.5 h-1.5 rounded-full ${pc.dot}`} />
                    <span className="text-[10px] text-slate-400">{pc.label}</span>
                  </span>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
