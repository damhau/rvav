import { Fragment } from "react";

/** A name that may wrap after its dots, hyphens, underscores and slashes, never inside a word. */
export function WordBreaks({ children }: { children: string }) {
  return children.split(/(?<=[._\-/])/).map((part, i) => (
    <Fragment key={i}>
      {i > 0 && <wbr />}
      {part}
    </Fragment>
  ));
}
