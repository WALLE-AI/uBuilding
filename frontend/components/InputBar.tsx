"use client";

import { useRef, useState, KeyboardEvent } from "react";
import { Send, Loader2, WifiOff, Paperclip, X, FileText, Video, Image } from "lucide-react";
import type { SendStatus, UploadedFile } from "@/types/chat";
import { uploadFile } from "@/utils/api";

const BACKEND = "http://localhost:8080";

interface InputBarProps {
  onSend: (content: string, attachments: UploadedFile[]) => void;
  status: SendStatus;
  connected: boolean;
  disabled?: boolean;
  pendingFiles: UploadedFile[];
  onFilesChange: (files: UploadedFile[]) => void;
}

function AttachmentChip({
  file,
  onRemove,
}: {
  file: UploadedFile;
  onRemove: () => void;
}) {
  return (
    <div className="relative group flex items-center gap-1.5 bg-slate-100 border border-slate-200 rounded-xl px-2.5 py-1.5 text-xs text-slate-600 max-w-[160px]">
      {file.category === "images" ? (
        file.localPreview ? (
          /* eslint-disable-next-line @next/next/no-img-element */
          <img
            src={file.localPreview}
            alt={file.name}
            className="w-6 h-6 rounded object-cover shrink-0"
          />
        ) : (
          <Image size={14} className="text-indigo-400 shrink-0" />
        )
      ) : file.category === "videos" ? (
        <Video size={14} className="text-purple-400 shrink-0" />
      ) : (
        <FileText size={14} className="text-slate-400 shrink-0" />
      )}
      <span className="truncate">{file.name}</span>
      <button
        onClick={onRemove}
        className="ml-0.5 text-slate-400 hover:text-slate-600 transition-colors shrink-0"
        title="移除附件"
      >
        <X size={12} />
      </button>
    </div>
  );
}

export default function InputBar({
  onSend,
  status,
  connected,
  disabled,
  pendingFiles,
  onFilesChange,
}: InputBarProps) {
  const textRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);

  const handleSend = () => {
    const value = textRef.current?.value.trim() ?? "";
    if ((!value && pendingFiles.length === 0) || status === "streaming") return;
    onSend(value, pendingFiles);
    if (textRef.current) {
      textRef.current.value = "";
      textRef.current.style.height = "auto";
    }
  };

  const handleKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleInput = () => {
    if (!textRef.current) return;
    textRef.current.style.height = "auto";
    textRef.current.style.height = `${Math.min(textRef.current.scrollHeight, 200)}px`;
  };

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    if (!files.length) return;
    e.target.value = "";

    setUploading(true);
    try {
      const results = await Promise.all(
        files.map(async (f) => {
          const uploaded = await uploadFile(f);
          if (uploaded.category === "images") {
            uploaded.localPreview = URL.createObjectURL(f);
          }
          return uploaded;
        })
      );
      onFilesChange([...pendingFiles, ...results]);
    } catch (err) {
      console.error("Upload failed:", err);
    } finally {
      setUploading(false);
    }
  };

  const removeFile = (idx: number) => {
    const next = pendingFiles.filter((_, i) => i !== idx);
    onFilesChange(next);
  };

  const isDisabled = disabled || status === "streaming" || !connected;

  return (
    <div className="bg-white border-t border-slate-100 px-6 py-4">
      <div className="max-w-4xl mx-auto">
        <div className="bg-white rounded-[20px] shadow-sm border border-slate-200 focus-within:shadow-md focus-within:border-indigo-200 transition-all">
          {/* Attachment preview strip */}
          {pendingFiles.length > 0 && (
            <div className="flex flex-wrap gap-2 px-4 pt-3">
              {pendingFiles.map((f, i) => (
                <AttachmentChip key={`${f.url}-${i}`} file={f} onRemove={() => removeFile(i)} />
              ))}
            </div>
          )}

          <textarea
            ref={textRef}
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
              {/* Attachment button */}
              <button
                type="button"
                onClick={() => fileRef.current?.click()}
                disabled={isDisabled || uploading}
                className="p-1.5 rounded-lg text-slate-400 hover:text-indigo-500 hover:bg-indigo-50 disabled:opacity-40 disabled:cursor-not-allowed transition-all"
                title="上传附件"
              >
                {uploading ? (
                  <Loader2 size={16} className="animate-spin" />
                ) : (
                  <Paperclip size={16} />
                )}
              </button>
              <input
                ref={fileRef}
                type="file"
                multiple
                accept="image/*,video/*,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.csv,.json,.zip,.rar"
                className="hidden"
                onChange={handleFileChange}
              />

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

        <p className="text-xs text-slate-300 text-center mt-2">
          AI 回复内容仅供参考，请自行核实。
        </p>
      </div>
    </div>
  );
}

export { BACKEND };
