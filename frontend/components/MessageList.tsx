"use client";

import { useEffect, useRef } from "react";
import ReactMarkdown from "react-markdown";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { oneDark } from "react-syntax-highlighter/dist/esm/styles/prism";
import { Bot, User } from "lucide-react";
import type { Message, StreamBlock } from "@/types/chat";
import ThinkingBlock from "./ThinkingBlock";
import ToolCallBlock from "./ToolCallBlock";

interface MessageListProps {
  messages: Message[];
  streamBlocks: StreamBlock[];
  isStreaming: boolean;
}

function MarkdownContent({ content }: { content: string }) {
  return (
    <div className="prose-chat text-sm leading-relaxed">
      <ReactMarkdown
        components={{
          code({ className, children, ...props }) {
            const match = /language-(\w+)/.exec(className || "");
            const isBlock = !!match;
            return isBlock ? (
              <SyntaxHighlighter
                style={oneDark as Record<string, React.CSSProperties>}
                language={match[1]}
                PreTag="div"
                className="!my-2 !rounded-lg !text-xs"
              >
                {String(children).replace(/\n$/, "")}
              </SyntaxHighlighter>
            ) : (
              <code
                className="bg-gray-700 text-pink-300 px-1 py-0.5 rounded text-xs font-mono"
                {...props}
              >
                {children}
              </code>
            );
          },
          p({ children }) {
            return <p className="mb-2 last:mb-0">{children}</p>;
          },
          ul({ children }) {
            return <ul className="list-disc list-inside mb-2 space-y-1">{children}</ul>;
          },
          ol({ children }) {
            return <ol className="list-decimal list-inside mb-2 space-y-1">{children}</ol>;
          },
          blockquote({ children }) {
            return (
              <blockquote className="border-l-2 border-indigo-500 pl-3 text-gray-400 italic my-2">
                {children}
              </blockquote>
            );
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}

function StreamBlocksRenderer({ blocks, isStreaming }: { blocks: StreamBlock[]; isStreaming: boolean }) {
  const hasText = blocks.some((b) => b.type === "text");
  const isLastBlock = (idx: number) => idx === blocks.length - 1;

  return (
    <div className="max-w-[80%] bg-gray-800 text-gray-100 rounded-2xl rounded-tl-sm px-4 py-3">
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
            <div key={`text-${idx}`}>
              <MarkdownContent content={block.content} />
              {isStreaming && isLastBlock(idx) && (
                <span className="inline-block w-1.5 h-4 bg-indigo-400 animate-pulse ml-0.5 align-text-bottom" />
              )}
            </div>
          );
        }
        return null;
      })}

      {isStreaming && !hasText && blocks.every((b) => b.type !== "text") && (
        <span className="flex gap-1 mt-1">
          {[0, 1, 2].map((i) => (
            <span
              key={i}
              className="w-2 h-2 bg-gray-500 rounded-full animate-bounce"
              style={{ animationDelay: `${i * 0.15}s` }}
            />
          ))}
        </span>
      )}
    </div>
  );
}

export default function MessageList({ messages, streamBlocks, isStreaming }: MessageListProps) {
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, streamBlocks]);

  const hasStreamContent = streamBlocks.length > 0;

  return (
    <div className="flex-1 overflow-y-auto px-4 py-6 space-y-6">
      {messages.length === 0 && !isStreaming && (
        <div className="flex flex-col items-center justify-center h-full text-center">
          <Bot size={48} className="text-gray-600 mb-4" />
          <p className="text-gray-500 text-lg font-medium">智能体对话</p>
          <p className="text-gray-600 text-sm mt-1">发送消息开始对话</p>
        </div>
      )}

      {messages.map((msg) => (
        <div
          key={msg.id}
          className={`flex gap-3 ${msg.role === "user" ? "flex-row-reverse" : "flex-row"}`}
        >
          <div
            className={`flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center ${
              msg.role === "user" ? "bg-indigo-600" : "bg-gray-700"
            }`}
          >
            {msg.role === "user" ? (
              <User size={16} className="text-white" />
            ) : (
              <Bot size={16} className="text-indigo-300" />
            )}
          </div>

          <div
            className={`max-w-[80%] rounded-2xl px-4 py-3 ${
              msg.role === "user"
                ? "bg-indigo-600 text-white rounded-tr-sm"
                : "bg-gray-800 text-gray-100 rounded-tl-sm"
            }`}
          >
            {msg.role === "user" ? (
              <p className="text-sm whitespace-pre-wrap">{msg.content}</p>
            ) : (
              <MarkdownContent content={msg.content} />
            )}
          </div>
        </div>
      ))}

      {isStreaming && hasStreamContent && (
        <div className="flex gap-3 flex-row">
          <div className="flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center bg-gray-700">
            <Bot size={16} className="text-indigo-300" />
          </div>
          <StreamBlocksRenderer blocks={streamBlocks} isStreaming={isStreaming} />
        </div>
      )}

      {isStreaming && !hasStreamContent && (
        <div className="flex gap-3 flex-row">
          <div className="flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center bg-gray-700">
            <Bot size={16} className="text-indigo-300" />
          </div>
          <div className="rounded-2xl rounded-tl-sm px-4 py-3 bg-gray-800">
            <span className="flex gap-1">
              {[0, 1, 2].map((i) => (
                <span
                  key={i}
                  className="w-2 h-2 bg-gray-500 rounded-full animate-bounce"
                  style={{ animationDelay: `${i * 0.15}s` }}
                />
              ))}
            </span>
          </div>
        </div>
      )}

      <div ref={bottomRef} />
    </div>
  );
}
