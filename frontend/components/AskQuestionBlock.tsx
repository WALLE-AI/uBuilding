"use client";

import { MessageCircleQuestion, Send, CheckCircle2 } from "lucide-react";
import { useState, useRef, useEffect } from "react";

interface AskQuestionBlockProps {
  requestId: string;
  question: string;
  options?: string[];
  answered?: string;
  onAnswer: (requestId: string, answer: string) => void;
}

export default function AskQuestionBlock({
  requestId,
  question,
  options,
  answered,
  onAnswer,
}: AskQuestionBlockProps) {
  const [inputVal, setInputVal] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const isAnswered = !!answered;

  useEffect(() => {
    if (!isAnswered && !options?.length) {
      inputRef.current?.focus();
    }
  }, [isAnswered, options]);

  const submit = (answer: string) => {
    if (!answer.trim() || isAnswered) return;
    setSelected(answer);
    onAnswer(requestId, answer.trim());
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit(inputVal);
    }
  };

  return (
    <div className="my-2 border border-violet-200 rounded-xl overflow-hidden bg-violet-50/30">
      {/* Header */}
      <div className="flex items-center gap-2.5 px-3 py-2.5 border-b border-violet-100">
        <span className="w-5 h-5 rounded-md bg-violet-100 flex items-center justify-center shrink-0">
          <MessageCircleQuestion size={12} className="text-violet-600" />
        </span>
        <span className="text-xs font-semibold text-violet-700">需要你的回答</span>

        {isAnswered && (
          <span className="ml-auto flex items-center gap-1 text-[11px] text-emerald-600 font-medium">
            <CheckCircle2 size={11} />
            已回答
          </span>
        )}
      </div>

      {/* Question text */}
      <div className="px-3 pt-2.5 pb-2">
        <p className="text-sm text-slate-700 leading-relaxed">{question}</p>
      </div>

      {isAnswered ? (
        /* Answered state */
        <div className="mx-3 mb-3 px-3 py-2 rounded-lg bg-emerald-50 border border-emerald-200">
          <p className="text-xs text-emerald-700 font-medium">你的回答：</p>
          <p className="text-sm text-emerald-800 mt-0.5">{answered}</p>
        </div>
      ) : options && options.length > 0 ? (
        /* Options mode */
        <div className="px-3 pb-3 space-y-1.5">
          {options.map((opt) => (
            <button
              key={opt}
              onClick={() => submit(opt)}
              disabled={!!selected}
              className={`w-full text-left text-sm px-3 py-2 rounded-lg border transition-all ${
                selected === opt
                  ? "bg-violet-100 border-violet-400 text-violet-800 font-medium"
                  : "bg-white border-slate-200 text-slate-700 hover:border-violet-300 hover:bg-violet-50 disabled:opacity-50"
              }`}
            >
              {opt}
            </button>
          ))}
          {/* Free-text fallback */}
          <div className="relative mt-2">
            <textarea
              ref={inputRef}
              rows={1}
              placeholder="或者输入自定义回答…"
              disabled={!!selected}
              value={inputVal}
              onChange={(e) => setInputVal(e.target.value)}
              onKeyDown={handleKeyDown}
              className="w-full resize-none text-sm px-3 py-2 pr-9 rounded-lg border border-slate-200 bg-white text-slate-700 placeholder:text-slate-400 outline-none focus:border-violet-300 focus:ring-1 focus:ring-violet-200 disabled:opacity-50 transition-colors"
            />
            <button
              onClick={() => submit(inputVal)}
              disabled={!inputVal.trim() || !!selected}
              className="absolute right-2 bottom-2 p-1 rounded-md text-violet-500 hover:text-violet-700 hover:bg-violet-100 disabled:opacity-30 transition-colors"
            >
              <Send size={13} />
            </button>
          </div>
        </div>
      ) : (
        /* Free text mode */
        <div className="px-3 pb-3 relative">
          <textarea
            ref={inputRef}
            rows={2}
            placeholder="输入你的回答…"
            value={inputVal}
            onChange={(e) => setInputVal(e.target.value)}
            onKeyDown={handleKeyDown}
            className="w-full resize-none text-sm px-3 py-2 pr-9 rounded-lg border border-slate-200 bg-white text-slate-700 placeholder:text-slate-400 outline-none focus:border-violet-300 focus:ring-1 focus:ring-violet-200 transition-colors"
          />
          <button
            onClick={() => submit(inputVal)}
            disabled={!inputVal.trim()}
            className="absolute right-5 bottom-5 p-1 rounded-md text-violet-500 hover:text-violet-700 hover:bg-violet-100 disabled:opacity-30 transition-colors"
          >
            <Send size={13} />
          </button>
        </div>
      )}
    </div>
  );
}
