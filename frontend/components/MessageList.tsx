"use client";

import { useEffect, useRef } from "react";
import { Sparkles } from "lucide-react";
import type { Message, StreamBlock } from "@/types/chat";
import ThinkingBlock from "./ThinkingBlock";
import ToolCallBlock from "./ToolCallBlock";
import MarkdownContent from "./MarkdownContent";

interface MessageListProps {
  messages: Message[];
  streamBlocks: StreamBlock[];
  isStreaming: boolean;
}

/* ── Avatar helpers ──────────────────────────────────────────────────────── */

function AssistantAvatar() {
  return (
    <div className="w-8 h-8 rounded-full bg-slate-900 flex items-center justify-center shrink-0 shadow-sm mt-1">
      <Sparkles size={15} className="text-white" />
    </div>
  );
}

function UserAvatar() {
  return (
    <div className="w-8 h-8 rounded-full bg-indigo-100 border border-indigo-200 flex items-center justify-center shrink-0 shadow-sm mt-1 overflow-hidden">
      <span className="text-xs font-bold text-indigo-600 select-none">U</span>
    </div>
  );
}

/* ── Streaming blocks renderer ───────────────────────────────────────────── */

function StreamBlocksRenderer({ blocks, isStreaming }: { blocks: StreamBlock[]; isStreaming: boolean }) {
  const hasText = blocks.some((b) => b.type === "text");
  const isLastBlock = (idx: number) => idx === blocks.length - 1;

  return (
    <div className="flex-1 min-w-0 text-slate-700 space-y-2 pt-1">
      {blocks.map((block, idx) => {
        if (block.type === "thinking") {
          return (
            <ThinkingBlock
              key={`thinking-${idx}`}
              content={block.content}
              streaming={block.streaming}
            />
          );
        }
        if (block.type === "tool_use") {
          return (
            <ToolCallBlock
              key={`tool-${block.id}`}
              name={block.name}
              status={block.status}
              input={block.input}
              result={block.result}
            />
          );
        }
        if (block.type === "text") {
          return (
            <div key={`text-${idx}`} className="text-[15px] leading-relaxed">
              <MarkdownContent content={block.content} />
              {isStreaming && isLastBlock(idx) && (
                <span className="inline-block w-1 h-4 bg-indigo-400 animate-pulse ml-0.5 align-text-bottom rounded-sm" />
              )}
            </div>
          );
        }
        return null;
      })}

      {/* Thinking dots when no text yet */}
      {isStreaming && !hasText && blocks.every((b) => b.type !== "text") && (
        <span className="flex gap-1 pt-1">
          {[0, 1, 2].map((i) => (
            <span
              key={i}
              className="w-2 h-2 bg-slate-300 rounded-full animate-bounce"
              style={{ animationDelay: `${i * 0.15}s` }}
            />
          ))}
        </span>
      )}
    </div>
  );
}

/* ── Main component ──────────────────────────────────────────────────────── */

export default function MessageList({ messages, streamBlocks, isStreaming }: MessageListProps) {
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, streamBlocks]);

  const hasStreamContent = streamBlocks.length > 0;

  return (
    <div className="flex-1 overflow-y-auto bg-white">
      <div className="max-w-4xl mx-auto px-6 py-6 space-y-6">

        {/* ── Empty state ── */}
        {messages.length === 0 && !isStreaming && (
          <div className="flex flex-col items-center justify-center py-24 text-center">
            <div className="w-16 h-16 rounded-2xl bg-slate-50 flex items-center justify-center mb-4 shadow-sm">
              <Sparkles size={32} className="text-slate-200" />
            </div>
            <p className="text-slate-400 font-medium">开始一次对话</p>
            <p className="text-slate-300 text-sm mt-1">发送消息，智能体将立即响应</p>
          </div>
        )}

        {/* ── Historical messages ── */}
        {messages.map((msg) => (
          <div
            key={msg.id}
            className={`msg-enter flex gap-4 ${msg.role === "user" ? "justify-end flex-row-reverse" : "justify-start"}`}
          >
            {msg.role === "assistant" ? <AssistantAvatar /> : <UserAvatar />}

            <div className={`w-full max-w-2xl ${msg.role === "user" ? "flex justify-end" : ""}`}>
              {msg.role === "user" ? (
                <div className="bg-slate-100 text-slate-800 px-4 py-2.5 rounded-[20px] shadow-sm text-[15px] whitespace-pre-wrap">
                  {msg.content}
                </div>
              ) : (
                <div className="text-slate-700 text-[15px] leading-relaxed space-y-2 pt-1">
                  <MarkdownContent content={msg.content} />
                </div>
              )}
            </div>
          </div>
        ))}

        {/* ── Streaming assistant message ── */}
        {isStreaming && hasStreamContent && (
          <div className="msg-enter flex gap-4 justify-start">
            <AssistantAvatar />
            <div className="w-full max-w-2xl">
              <StreamBlocksRenderer blocks={streamBlocks} isStreaming={isStreaming} />
            </div>
          </div>
        )}

        {/* ── Waiting dots (no blocks yet) ── */}
        {isStreaming && !hasStreamContent && (
          <div className="msg-enter flex gap-4 justify-start">
            <AssistantAvatar />
            <div className="pt-3 flex gap-1">
              {[0, 1, 2].map((i) => (
                <span
                  key={i}
                  className="w-2 h-2 bg-slate-300 rounded-full animate-bounce"
                  style={{ animationDelay: `${i * 0.15}s` }}
                />
              ))}
            </div>
          </div>
        )}

        <div ref={bottomRef} />
      </div>
    </div>
  );
}
