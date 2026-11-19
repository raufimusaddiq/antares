declare module "react-syntax-highlighter/dist/esm/prism" {
  import type { ComponentType, ReactNode } from "react";
  interface PrismProps {
    language?: string;
    style?: Record<string, unknown>;
    PreTag?: string | ComponentType<any>;
    className?: string;
    children?: ReactNode;
  }
  const Prism: ComponentType<PrismProps>;
  export default Prism;
}
