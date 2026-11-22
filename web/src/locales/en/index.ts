// Every namespace of this language, in one chunk (ADR-0061).
export default import.meta.glob<Record<string, unknown>>("./*.json", { eager: true, import: "default" });
