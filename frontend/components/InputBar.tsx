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
    <div className="border-t border-gray-800 px-4 py-4 bg-gray-950">
      {!connected && (
        <div className="flex items-center gap-2 text-yellow-500 text-xs mb-2">
          <WifiOff size={12} />
          <span>正在重连后端...</span>
        </div>
      )}
      <div className="flex items-end gap-3 bg-gray-800 rounded-2xl px-4 py-3 border border-gray-700 focus-within:border-indigo-500 transition-colors">
        <textarea
          ref={ref}
          rows={1}
          placeholder={connected ? "输入消息… (Enter 发送，Shift+Enter 换行)" : "等待连接..."}
          disabled={isDisabled}
          onKeyDown={handleKey}
          onInput={handleInput}
          className="flex-1 bg-transparent text-gray-100 placeholder-gray-500 text-sm resize-none outline-none min-h-[24px] max-h-[200px] disabled:opacity-50"
        />
        <button
          onClick={handleSend}
          disabled={isDisabled}
          className="flex-shrink-0 w-8 h-8 rounded-xl bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 disabled:cursor-not-allowed flex items-center justify-center transition-colors"
        >
          {status === "streaming" ? (
            <Loader2 size={14} className="text-white animate-spin" />
          ) : (
            <Send size={14} className="text-white" />
          )}
        </button>
      </div>
      <p className="text-xs text-gray-600 text-center mt-2">
        AI 回复内容仅供参考，请自行核实。
      </p>
    </div>
  );
}
