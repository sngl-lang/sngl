/** Add two numbers. */
export function add(a: number, b: number): number {
    return a + b;
}

/** Greet by name. */
export function greet(name: string): string {
    return `hello ${name}`;
}

export async function fetchTitle(url: string): Promise<string> {
    return "title";
}
