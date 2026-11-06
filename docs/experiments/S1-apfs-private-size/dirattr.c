// dirattr: ask a DIRECTORY for the extended private-size attribute only.
#include <stdio.h>
#include <unistd.h>
#include <string.h>
#include <stdint.h>
#include <sys/attr.h>
int main(int argc, char **argv) {
	struct attrlist al; memset(&al, 0, sizeof al);
	al.bitmapcount = ATTR_BIT_MAP_COUNT;
	al.commonattr = ATTR_CMN_RETURNED_ATTRS;
	al.forkattr = ATTR_CMNEXT_PRIVATESIZE;
	struct { uint32_t len; attribute_set_t ret; uint8_t rest[64]; } __attribute__((packed)) r;
	memset(&r, 0, sizeof r);
	if (getattrlist(argv[1], &al, &r, sizeof r, FSOPT_NOFOLLOW | FSOPT_ATTR_CMN_EXTENDED) != 0) { perror("getattrlist"); return 1; }
	printf("returned fork bits: 0x%x  privatesize present: %s\n", r.ret.forkattr, (r.ret.forkattr & ATTR_CMNEXT_PRIVATESIZE) ? "yes" : "NO");
	if (r.ret.forkattr & ATTR_CMNEXT_PRIVATESIZE) { int64_t v; memcpy(&v, r.rest, 8); printf("privatesize of the directory itself: %lld\n", (long long)v); }
	return 0;
}
