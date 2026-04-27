export interface User {
    id: number;
    name: string;
    email: string | null;
    tags: string[];
}

export function makeUser(id: number, name: string): User {
    return { id, name, email: null, tags: [] };
}
