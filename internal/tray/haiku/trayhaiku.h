// SPDX-License-Identifier: Unlicense OR MIT

// libtrayhaiku: the client's icon in the Deskbar. The library is a Deskbar
// add-on, which the Deskbar loads into its own process and asks for a view
// (instantiate_deskbar_item); the client loads it too, with dlopen, to add
// and remove that item. The two talk over a port named TH_PORT, which the
// client makes: the icon writes to it, and the client answers on the port
// a message names.

#ifndef TRAYHAIKU_H
#define TRAYHAIKU_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// TH_ABI changes with the interface: the client checks th_abi against it.
#define TH_ABI 1

// TH_NAME names the item in the Deskbar; TH_PORT is the client's port;
// TH_SIGNATURE is the client's, whose icon the item shows.
#define TH_NAME "KomaruGram Go"
#define TH_PORT "komarugram-go tray"
#define TH_SIGNATURE "application/x-vnd.komarugram-go"

// What the icon writes to the client's port.
enum {
	// A click on the icon; no data.
	TH_ACTIVATE = 'actv',
	// The icon asks for its tooltip and menu; the data is the int32 port
	// to answer on, with a TH_MENU message: NUL-ended strings, the
	// tooltip, then each item's label, an empty one for a separator.
	TH_MENU = 'menu',
	// An item of the menu was chosen; the data is its int32 index.
	TH_ITEM = 'item',
};

int32_t th_abi(void);

// th_add adds the item from the add-on at path, unless it is there
// already; th_remove removes it; th_shown reports whether the Deskbar
// shows it. th_add returns 0, or a Haiku status_t.
int32_t th_add(const char *path);
void th_remove(void);
int32_t th_shown(void);

#ifdef __cplusplus
}
#endif

#endif
