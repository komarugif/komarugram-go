// SPDX-License-Identifier: Unlicense OR MIT

// The Deskbar's item of the client: its icon, a click that brings the
// client's windows back, and a menu. It runs in the Deskbar's process and
// knows the client only by its port (trayhaiku.h).

#include "trayhaiku.h"

#include <AppFileInfo.h>
#include <Bitmap.h>
#include <Deskbar.h>
#include <Entry.h>
#include <File.h>
#include <MenuItem.h>
#include <Message.h>
#include <MessageRunner.h>
#include <Messenger.h>
#include <OS.h>
#include <PopUpMenu.h>
#include <Roster.h>
#include <View.h>
#include <Window.h>

#include <string.h>
#include <vector>
#include <string>

namespace {

const uint32 kCheck = 'thck';
const uint32 kChosen = 'thch';
// The client is looked for this often; when it is gone, the item goes.
const bigtime_t kCheckInterval = 2000000;

// Ask writes TH_MENU to the client and returns its answer's strings: the
// tooltip first, then the items' labels. It returns nothing when the
// client does not answer within a second.
std::vector<std::string> Ask() {
	std::vector<std::string> out;
	port_id client = find_port(TH_PORT);
	if (client < 0)
		return out;
	port_id reply = create_port(1, "komarugram-go tray reply");
	if (reply < 0)
		return out;
	int32 r = reply;
	if (write_port_etc(client, TH_MENU, &r, sizeof(r), B_RELATIVE_TIMEOUT, 1000000) == B_OK) {
		ssize_t size = port_buffer_size_etc(reply, B_RELATIVE_TIMEOUT, 1000000);
		if (size > 0) {
			std::vector<char> buf(size);
			int32 code;
			if (read_port_etc(reply, &code, buf.data(), size, B_RELATIVE_TIMEOUT, 1000000) == size && code == TH_MENU) {
				for (ssize_t i = 0; i < size;) {
					const char *s = buf.data() + i;
					size_t n = strnlen(s, size - i);
					out.push_back(std::string(s, n));
					i += n + 1;
				}
			}
		}
	}
	delete_port(reply);
	return out;
}

void Tell(int32 code, const void *data, size_t size) {
	port_id client = find_port(TH_PORT);
	if (client >= 0)
		write_port_etc(client, code, data, size, B_RELATIVE_TIMEOUT, 1000000);
}

} // namespace

// KomaruGramTrayView is the item. The Deskbar archives the view it gets
// and makes it anew from the archive, finding the class by its name: by
// the symbol of its Instantiate, in the images it has loaded, this one
// among them. The name is the client's so that it meets no other's.
class _EXPORT KomaruGramTrayView : public BView {
public:
	KomaruGramTrayView(BRect frame) : BView(frame, TH_NAME, B_FOLLOW_NONE, B_WILL_DRAW) {}
	KomaruGramTrayView(BMessage *archive) : BView(archive) {}
	~KomaruGramTrayView() { delete fIcon; }

	static BArchivable *Instantiate(BMessage *archive);

	void AttachedToWindow() {
		BView::AttachedToWindow();
		AdoptParentColors();
		LoadIcon();
		std::vector<std::string> answer = Ask();
		if (!answer.empty())
			SetToolTip(answer[0].c_str());
		BMessage check(kCheck);
		fRunner = new BMessageRunner(BMessenger(this), &check, kCheckInterval);
	}

	void DetachedFromWindow() {
		delete fRunner;
		fRunner = NULL;
		BView::DetachedFromWindow();
	}

	void Draw(BRect) {
		if (fIcon == NULL)
			return;
		SetDrawingMode(B_OP_ALPHA);
		SetBlendingMode(B_PIXEL_ALPHA, B_ALPHA_OVERLAY);
		DrawBitmap(fIcon, Bounds());
		SetDrawingMode(B_OP_COPY);
	}

	void MouseDown(BPoint where) {
		int32 buttons = 0;
		if (Window()->CurrentMessage() != NULL)
			Window()->CurrentMessage()->FindInt32("buttons", &buttons);
		if (buttons & B_SECONDARY_MOUSE_BUTTON)
			ShowMenu(where);
		else
			Tell(TH_ACTIVATE, NULL, 0);
	}

	void MessageReceived(BMessage *message) {
		switch (message->what) {
		case kCheck:
			// The client is gone, crashed or killed: an item the Deskbar
			// keeps in its settings would otherwise come back with it.
			// The Deskbar does not answer RemoveItem, so this waits for
			// nothing.
			if (find_port(TH_PORT) < 0)
				BDeskbar().RemoveItem(TH_NAME);
			break;
		case kChosen: {
			int32 index;
			if (message->FindInt32("index", &index) == B_OK)
				Tell(TH_ITEM, &index, sizeof(index));
			break;
		}
		default:
			BView::MessageReceived(message);
		}
	}

private:
	void LoadIcon() {
		delete fIcon;
		fIcon = NULL;
		entry_ref ref;
		if (be_roster->FindApp(TH_SIGNATURE, &ref) != B_OK)
			return;
		BFile file(&ref, B_READ_ONLY);
		BAppFileInfo info(&file);
		if (info.InitCheck() != B_OK)
			return;
		BRect b = Bounds();
		int32 size = (int32)(b.Width() < b.Height() ? b.Width() : b.Height()) + 1;
		BBitmap *icon = new BBitmap(BRect(0, 0, size - 1, size - 1), B_RGBA32);
		if (info.GetIcon(icon, (icon_size)size) != B_OK) {
			delete icon;
			return;
		}
		fIcon = icon;
	}

	void ShowMenu(BPoint where) {
		std::vector<std::string> answer = Ask();
		if (answer.size() < 2)
			return;
		BPopUpMenu *menu = new BPopUpMenu("tray", false, false);
		for (size_t i = 1; i < answer.size(); i++) {
			if (answer[i].empty()) {
				menu->AddSeparatorItem();
				continue;
			}
			BMessage *chosen = new BMessage(kChosen);
			chosen->AddInt32("index", (int32)(i - 1));
			menu->AddItem(new BMenuItem(answer[i].c_str(), chosen));
		}
		menu->SetTargetForItems(this);
		// Asynchronous: the Deskbar's window goes on while the menu is
		// open, and the menu deletes itself.
		menu->Go(ConvertToScreen(where), true, true, true);
	}

	BBitmap *fIcon = NULL;
	BMessageRunner *fRunner = NULL;
};

// Instantiate is defined here, not in the class, so that it is in the
// library for the Deskbar to find, though nothing here calls it.
BArchivable *KomaruGramTrayView::Instantiate(BMessage *archive) {
	if (!validate_instantiation(archive, "KomaruGramTrayView"))
		return NULL;
	return new KomaruGramTrayView(archive);
}

extern "C" _EXPORT BView *instantiate_deskbar_item(float maxWidth, float maxHeight) {
	float side = maxHeight < maxWidth ? maxHeight : maxWidth;
	return new KomaruGramTrayView(BRect(0, 0, side - 1, side - 1));
}

extern "C" {

int32_t th_abi(void) { return TH_ABI; }

int32_t th_add(const char *path) {
	BDeskbar deskbar;
	if (deskbar.HasItem(TH_NAME))
		return B_OK;
	entry_ref ref;
	status_t st = get_ref_for_path(path, &ref);
	if (st != B_OK)
		return st;
	return deskbar.AddItem(&ref);
}

void th_remove(void) { BDeskbar().RemoveItem(TH_NAME); }

int32_t th_shown(void) { return BDeskbar().HasItem(TH_NAME) ? 1 : 0; }

} // extern "C"
