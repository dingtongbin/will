#include <stdlib.h>

#include "api.h"

Box* make_box(int v) {
    Box* box = (Box*)malloc(sizeof(Box));

    box->v = v;
    return box;
}

void drop_box(Box* box) {
    free(box);
}
