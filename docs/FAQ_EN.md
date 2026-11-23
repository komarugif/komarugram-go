**Q: Why does this exist?**  
**A:** The official Telegram Desktop and its forks have many issues: heavy reliance on the C++ codebase and libraries ([TDLib](https://github.com/tdlib/td), Qt), a build process that is time-consuming and resource-intensive, and a high barrier to entry for developing your own modifications. In addition, it uses a copyleft license — our repository is completely free in terms of how you distribute it.  

**Q: Is this a vibe code?**  
**A:** Yes. The code in this project was primarily written by Claude Opus 5.5 large language model. The maintainer is responsible for the concept, selection of the technology stack and libraries, and quality control.

**Q: Is this a port of Telegram Desktop in Go?**  
**A:** No, this is a completely independent project, written from scratch. We do not use Telegram Desktop as a single reference. While some features have been ported from the Android version of Telegram, many others have been implemented at our own discretion.  
If you are looking for a similar project based on Telegram Desktop, take a look at [svatoshgpt/komarugram](https://github.com/svatoshgpt/komarugram)

**Q: Why not Rust, if you’re working with Claude anyway?**  
**A:** We have nothing against Rust — it’s an excellent tool, and it should be incorporated into projects whenever possible. In fact, I’m glad that vibe coders have started favoring Rust alongside Python. However, Go is now mature enough for desktop applications, so there’s no longer any reason not to use it — especially given the useful set of libraries we have at our disposal. Go makes it possible for anyone interested to build and modify this project.  
If you’re looking for a similar project written in Rust, take a look at [shinycake/quill](https://github.com/shinycake/quill)

**Q: Is it safe to log in to this project using my main account?**  
**A:** Yes, absolutely. We care about your privacy and security, and we do our best to prevent your session from being compromised as far as possible. However, some features, such as "Sticker set creator", access third-party services to provide additional functionality. No logs or requests to third-party services will be sent automatically, and your login session never leaves your device.

**Q: What is the risk that the account will be frozen or the session will be terminated?**  
**A:** For older accounts, the risk is zero. For new accounts, we do not recommend enabling "ghost mode." Telegram recognises your session as an official Telegram Desktop instance (AyuGram does the same). While this minimizes risk, our implementation differs significantly from the official Telegram Desktop; the behavior and sequence of requests differ and may not match with how the official client operates.

**Q: Is this a finished application for everyday use?**  
**A:** No, this is an experiment. Bugs and race conditions can occur here, and some attachments may fail to load. Nevertheless, we aim to implement all the features available in the official clients, while also making the application and its windows more resilient to crashes than Telegram Desktop.
