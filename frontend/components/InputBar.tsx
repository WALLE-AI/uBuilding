"use client";

import { useRef, KeyboardEvent } from "react";
import { Send, Loader2, WifiOff } from "lucide-react";
import type { SendStatus } from "@/types/chat";

interface InputBarProps {
  onSend: (content: string) => void;
  status: SendStatus;
  connected: boolean;
  disabled?: boolean;
}

export default function InputBar({ onSend, status, connected, disabled }: InputBarProps) {
  const ref = useRef<HTMLTextAreaElement>(null);

  const handleSend = () => {
    const value = ref.current?.value.trim();
    if (!value || status === "streaming") return;
    onSend(value);
    if (ref.current) ref.current.value = "";
    ref.current?.style && (ref.current.style.height = "auto");
  };

  const handleKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleInput = () => {
    if (!ref.current) return;
    ref.current.style.height = "auto";
    ref.current.style.height = `${Math.min(ref.current.scrollHeight, 200)}px`;
  };

  const isDisabled = disabled || status === "streaming" || !connected;

  return (
    <div className="bg-white border-t border-slate-100 px-6 py-4">
      <div className="max-w-4xl mx-auto">
        {/* White card input */}
        <div className="bg-white rounded-[20px] shadow-sm border border-slate-200 focus-within:shadow-md focus-within:border-indigo-200 transition-all">
          <textarea
            ref={ref}
            rows={1}
            placeholder={connected ? "发送消息…" : "等待连接..."}
            disabled={isDisabled}
            onKeyDown={handleKey}
            onInput={handleInput}
            className="w-full bg-transparent border-none outline-none focus:outline-none focus:ring-0 text-slate-700 placeholder-slate-400 p-4 min-h-[60px] max-h-[200px] resize-none text-[15px] rounded-t-[20px] disabled:opacity-50"
          />
          {/* Bottom toolbar */}
          <div className="flex items-center justify-between px-4 py-3">
            <div className="flex items-center gap-2">
              {!connected && (
                <span className="flex items-center gap-1.5 text-xs text-amber-500">
                  <WifiOff size={12} />
                  正在重连...
                </span>
              )}
              {connected && (
                <span className="text-xs text-slate-300">
                  Enter 发送&nbsp;·&nbsp;Shift+Enter 换行
                </span>
              )}
            </div>
            <button
              onClick={handleSend}
              disabled={isDisabled}
              className="bg-indigo-600 hover:bg-indigo-500 active:scale-95 disabled:opacity-40 disabled:cursor-not-allowed p-2 rounded-xl text-white transition-all shadow-sm shadow-indigo-500/20"
              title={status === "streaming" ? "正在生成..." : "发送消息"}
            >
              {status === "streaming" ? (
                <Loader2 size={18} className="animate-spin" />
              ) : (
                <Send size={18} />
              )}
            </button>
          </div>
        </div>

        {/* Disclaimer */}
        <p className="text-xs text-slate-300 text-center mt-2">
          AI 回复内容仅供参考，请自行核实。
        </p>
      </div>
    </div>
  );
}
