typedef struct Box {
    int v;
} Box;

Box* make_box(int v);
void drop_box(Box* box);
